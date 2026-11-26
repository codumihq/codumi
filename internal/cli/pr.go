package cli

// codumi pr: o comentário de revisão de um pull request, em Markdown. Junta o
// impacto da mudança (quem chama, rotas, telas, banco, testes) e os achados
// da auditoria que caem nos arquivos do PR. Com --fail-on, sai com erro quando
// o PR traz um achado desse nível (para travar o merge no GitHub).

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/audit"
	"github.com/codumihq/codumi/internal/store"
)

// prMarker identifica o comentário do Codumi (a Action atualiza o mesmo, sem empilhar).
const prMarker = "<!-- codumi-pr -->"

var errPRFail = errors.New("this pull request brings audit findings at the --fail-on level")

func cmdPR(pos []string, flags map[string]string) error {
	pt := strings.HasPrefix(flags["lang"], "pt")
	t := func(en, p string) string {
		if pt {
			return p
		}
		return en
	}
	// o impacto, como texto (o mesmo do codumi impact)
	var ib bytes.Buffer
	prevOut := out
	out = &ib
	err := cmdImpact(pos, map[string]string{"base": flags["base"], "depth": flags["depth"], "limit": "15"})
	out = prevOut
	if err != nil {
		return err
	}
	root, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	changes, _ := gitChanges(root, flags["base"])
	g, err := store.LoadGraph(db)
	if err != nil {
		return err
	}
	var inPR []audit.Finding
	other := 0
	for _, f := range audit.Run(root, g) {
		file := f.Where
		if i := strings.LastIndex(file, ":"); i > 0 {
			file = file[:i]
		}
		if _, ok := changes[file]; ok && file != "" {
			inPR = append(inPR, f)
		} else {
			other++
		}
	}
	sort.SliceStable(inPR, func(i, j int) bool { return inPR[i].Severity > inPR[j].Severity })

	impact := strings.TrimSpace(ib.String())
	fmt.Fprintln(out, prMarker)
	fmt.Fprintf(out, "### %s\n\n", t("Codumi · what this pull request reaches", "Codumi · o que este PR alcança"))
	if strings.HasPrefix(impact, "no changes") {
		fmt.Fprintln(out, t("No code changes to map.", "Nenhuma mudança de código para mapear."))
	} else {
		head := impact
		if i := strings.Index(impact, "\n"); i > 0 {
			head = impact[:i]
		}
		fmt.Fprintf(out, "%s\n\n", head)
		fmt.Fprintf(out, "<details open><summary>%s</summary>\n\n```text\n%s\n```\n\n</details>\n\n", t("Callers, routes, pages, database and tests", "Quem chama, rotas, telas, banco e testes"), strings.TrimSpace(strings.TrimPrefix(impact, head)))
	}
	fmt.Fprintf(out, "#### %s\n\n", t("Security in the changed files", "Segurança nos arquivos do PR"))
	if len(inPR) == 0 {
		fmt.Fprintln(out, t("No audit finding in the files this pull request changes.", "Nenhum achado da auditoria nos arquivos que este PR muda."))
	}
	icon := map[audit.Severity]string{audit.High: "🔴", audit.Medium: "🟡", audit.Low: "⚪"}
	for _, f := range inPR {
		fmt.Fprintf(out, "- %s **%s** · %s · `%s` %s — `%s`", icon[f.Severity], f.Level, f.Rule, f.Target, f.Message, f.Where)
		if f.Fix != "" {
			fmt.Fprintf(out, "\n  - %s %s", t("Fix:", "Conserto:"), f.Fix)
		}
		fmt.Fprintln(out)
	}
	if other > 0 {
		fmt.Fprintf(out, "\n%s\n", t(fmt.Sprintf("_%d other findings already existed outside this pull request (`codumi audit`)._", other), fmt.Sprintf("_%d outros achados já existiam fora deste PR (`codumi audit`)._", other)))
	}
	fmt.Fprintf(out, "\n<sub>%s</sub>\n", t("Mapped by [Codumi](https://codumi.dev): exact call graph, nothing leaves the runner.", "Mapeado pelo [Codumi](https://codumi.dev): grafo exato de chamadas, nada sai da máquina do CI."))

	lvl := map[string]audit.Severity{"high": audit.High, "medium": audit.Medium, "low": audit.Low}
	if m, ok := lvl[flags["fail-on"]]; ok {
		for _, f := range inPR {
			if f.Severity >= m {
				return errPRFail
			}
		}
	}
	return nil
}
