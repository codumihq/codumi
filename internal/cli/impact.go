package cli

// codumi impact: o que uma mudança afeta. Lê o diff do git (o que ainda não
// foi commitado, ou desde --base), acha as declarações cujas linhas mudaram e
// sobe o grafo de chamadas: quem chama, as rotas e telas que chegam nelas, o
// que elas tocam no banco, as rotas da API que o código mudado chama e os
// testes no caminho. As migrações alteradas entram com as tabelas e funções
// que declaram.

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type lineSpan struct{ from, to int }

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// gitChanges devolve, por arquivo (relativo à raiz), as linhas novas que
// mudaram. Arquivos novos (sem rastrear) contam inteiros.
func gitChanges(root, base string) (map[string][]lineSpan, error) {
	args := []string{"-C", root, "diff", "-U0", "--no-color", "--no-ext-diff"}
	if base != "" {
		args = append(args, base)
	} else {
		args = append(args, "HEAD")
	}
	outB, err := exec.Command("git", args...).Output()
	if err != nil {
		// repositório sem commits: tudo que existe é novo
		if base == "" {
			outB, err = exec.Command("git", "-C", root, "diff", "-U0", "--no-color", "--cached").Output()
		}
		if err != nil {
			return nil, fmt.Errorf("git diff failed: is %s a git repository?", root)
		}
	}
	ch := map[string][]lineSpan{}
	file, oldFile := "", ""
	sc := bufio.NewScanner(bytes.NewReader(outB))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "--- "):
			oldFile = strings.TrimPrefix(strings.TrimPrefix(l, "--- "), "a/")
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			if file == "/dev/null" {
				// arquivo apagado: tudo que ele declarava mudou
				file = ""
				if oldFile != "/dev/null" {
					ch[oldFile] = append(ch[oldFile], lineSpan{0, 0})
				}
			}
		case strings.HasPrefix(l, "@@") && file != "":
			m := hunkRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[3])
			n := 1
			if m[4] != "" {
				n, _ = strconv.Atoi(m[4])
			}
			if n == 0 { // só remoção: marca a linha onde o trecho sumiu
				ch[file] = append(ch[file], lineSpan{start, start + 1})
			} else {
				ch[file] = append(ch[file], lineSpan{start, start + n - 1})
			}
		}
	}
	// arquivos novos ainda sem rastrear também são mudança
	if u, err := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard").Output(); err == nil {
		for _, f := range strings.Split(strings.TrimSpace(string(u)), "\n") {
			if f != "" {
				ch[f] = []lineSpan{{1, 1 << 30}}
			}
		}
	}
	return ch, nil
}

type declSpan struct {
	name, kind string
	from, to   int
}

// declSpans dá a faixa de linhas de cada declaração de um arquivo: da sua
// linha até a linha antes da próxima declaração do mesmo nível.
func declSpans(db *sql.DB, file string) []declSpan {
	rows, err := db.Query(`SELECT name, kind, line FROM decls WHERE file = ? ORDER BY line`, file)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ds []declSpan
	for rows.Next() {
		var d declSpan
		rows.Scan(&d.name, &d.kind, &d.from)
		ds = append(ds, d)
	}
	// membros (Classe.m) fecham na próxima declaração; o topo, no próximo do topo
	for i := range ds {
		ds[i].to = 1 << 30
		member := strings.Contains(ds[i].name, ".")
		for j := i + 1; j < len(ds); j++ {
			if ds[j].from > ds[i].from && (member || !strings.Contains(ds[j].name, ".")) {
				ds[i].to = ds[j].from - 1
				break
			}
		}
	}
	return ds
}

type node struct{ file, name string }

func isTestFile(f string) bool {
	b := filepath.Base(f)
	return strings.HasSuffix(b, "_test.go") || strings.Contains(b, ".test.") || strings.Contains(b, ".spec.") ||
		strings.HasPrefix(b, "test_") || strings.HasSuffix(b, "_test.py") || strings.HasSuffix(b, "_test.dart") ||
		strings.Contains(f, "/__tests__/") || strings.HasSuffix(b, "Test.kt") || strings.HasSuffix(b, "Test.java") || strings.HasSuffix(b, "Tests.cs")
}

func cmdImpact(pos []string, flags map[string]string) error {
	// o mapa precisa estar em dia com o que está no disco
	cwd := "."
	if len(pos) > 0 {
		cwd = pos[0]
	}
	cmdIndex([]string{cwd}, map[string]string{"quiet": "true"})
	root, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	changes, err := gitChanges(root, flags["base"])
	if err != nil {
		return err
	}
	depth := intFlag(flags, "depth", 4)
	limit := intFlag(flags, "limit", 25)

	changed := map[node]bool{}
	var changedList []node
	var changedFiles, migrations []string
	for f, spans := range changes {
		changedFiles = append(changedFiles, f)
		if strings.HasSuffix(f, ".sql") {
			migrations = append(migrations, f)
		}
		ds := declSpans(db, f)
		spans = nonBlank(filepath.Join(root, f), spans)
		for _, d := range ds {
			for _, s := range spans {
				if s.from == 0 || s.from <= d.to && s.to >= d.from {
					n := node{f, d.name}
					if !changed[n] {
						changed[n] = true
						changedList = append(changedList, n)
					}
					break
				}
			}
		}
	}
	// mexeu dentro de um método: vale o método, não a classe inteira
	var kept []node
	for _, n := range changedList {
		inner := false
		for _, m := range changedList {
			if m.file == n.file && strings.HasPrefix(m.name, n.name+".") {
				inner = true
				break
			}
		}
		if inner {
			delete(changed, n)
		} else {
			kept = append(kept, n)
		}
	}
	changedList = kept
	sort.Strings(changedFiles)
	if len(changedFiles) == 0 {
		fmt.Fprintln(out, "no changes", map[bool]string{true: "since " + flags["base"], false: "in the working tree"}[flags["base"] != ""])
		return nil
	}
	sort.Slice(changedList, func(i, j int) bool {
		if changedList[i].file != changedList[j].file {
			return changedList[i].file < changedList[j].file
		}
		return changedList[i].name < changedList[j].name
	})

	// sobe o grafo: quem chama o que mudou, até depth níveis
	type hop struct {
		via   node
		level int
	}
	seen := map[node]hop{}
	for _, n := range changedList {
		seen[n] = hop{level: 0}
	}
	frontier := changedList
	for lvl := 1; lvl <= depth && len(frontier) > 0; lvl++ {
		var next []node
		for _, n := range frontier {
			rows, err := db.Query(`SELECT DISTINCT file, from_name FROM refs WHERE to_file = ? AND to_name = ? AND kind NOT IN ('import','export')`, n.file, n.name)
			if err != nil {
				continue
			}
			for rows.Next() {
				var c node
				rows.Scan(&c.file, &c.name)
				if c.name == "" || c.name == "<module>" {
					c.name = "<module>"
				}
				if _, ok := seen[c]; ok {
					continue
				}
				seen[c] = hop{via: n, level: lvl}
				next = append(next, c)
			}
			rows.Close()
		}
		frontier = next
	}
	var callers []node
	for n, h := range seen {
		if h.level > 0 {
			callers = append(callers, n)
		}
	}
	sort.Slice(callers, func(i, j int) bool {
		if seen[callers[i]].level != seen[callers[j]].level {
			return seen[callers[i]].level < seen[callers[j]].level
		}
		return callers[i].file+callers[i].name < callers[j].file+callers[j].name
	})
	pathTo := func(n node) string {
		var p []string
		for cur, ok := n, true; ok; {
			p = append(p, cur.name)
			h := seen[cur]
			if h.level == 0 {
				break
			}
			cur, ok = h.via, true
		}
		return strings.Join(p, " → ")
	}
	inSet := func(file, name string) bool { _, ok := seen[node{file, name}]; return ok }

	// rotas e telas que chegam no que mudou
	type hit struct{ label, path string }
	var routes, pages []hit
	if rows, err := db.Query(`SELECT method, path, handler_file, handler FROM routes`); err == nil {
		for rows.Next() {
			var m, p, hf, h string
			rows.Scan(&m, &p, &hf, &h)
			if inSet(hf, h) {
				routes = append(routes, hit{strings.TrimSpace(m + " " + p), pathTo(node{hf, h})})
			}
		}
		rows.Close()
	}
	if rows, err := db.Query(`SELECT path, file, component FROM pages`); err == nil {
		for rows.Next() {
			var p, f, c string
			rows.Scan(&p, &f, &c)
			if inSet(f, c) {
				pages = append(pages, hit{p, pathTo(node{f, c})})
			}
		}
		rows.Close()
	}
	// o que o código mudado toca no banco e quais rotas da API ele chama
	dbs := map[string]map[string]bool{}
	var calls []string
	callSeen := map[string]bool{}
	for _, n := range changedList {
		if rows, err := db.Query(`SELECT type, name, ops FROM db_uses WHERE file = ? AND from_name = ?`, n.file, n.name); err == nil {
			for rows.Next() {
				var t, nm, ops string
				rows.Scan(&t, &nm, &ops)
				k := t + " " + nm
				if dbs[k] == nil {
					dbs[k] = map[string]bool{}
				}
				for _, o := range strings.Split(ops, ",") {
					if o = strings.TrimSpace(o); o != "" {
						dbs[k][o] = true
					}
				}
			}
			rows.Close()
		}
		if rows, err := db.Query(`SELECT method, path FROM route_calls WHERE file = ? AND from_name = ?`, n.file, n.name); err == nil {
			for rows.Next() {
				var m, p string
				rows.Scan(&m, &p)
				k := strings.TrimSpace(m + " " + p)
				if !callSeen[k] {
					callSeen[k] = true
					calls = append(calls, k)
				}
			}
			rows.Close()
		}
	}
	// testes no caminho (arquivos de teste entre quem chama ou entre os mudados)
	testSet := map[string]bool{}
	for n := range seen {
		if isTestFile(n.file) {
			testSet[n.file] = true
		}
	}
	for _, f := range changedFiles {
		if isTestFile(f) {
			testSet[f] = true
		}
	}
	// migrações: o que elas declaram
	var schema []string
	for _, m := range migrations {
		for _, q := range []string{`SELECT 'table ' || schema || '.' || name FROM sql_tables WHERE src LIKE ?`, `SELECT 'function ' || schema || '.' || name FROM sql_functions WHERE src LIKE ?`, `SELECT 'policy ' || name || ' on ' || table_name FROM sql_policies WHERE src LIKE ?`} {
			if rows, err := db.Query(q, m+":%"); err == nil {
				for rows.Next() {
					var s string
					rows.Scan(&s)
					schema = append(schema, s)
				}
				rows.Close()
			}
		}
	}

	// ---- saída ----
	fmt.Fprintf(out, "%d changed files, %d changed declarations", len(changedFiles), len(changedList))
	if flags["base"] != "" {
		fmt.Fprintf(out, " since %s", flags["base"])
	}
	fmt.Fprintln(out)
	cap := func(n int) int {
		if limit > 0 && n > limit {
			return limit
		}
		return n
	}
	byFile := map[string][]string{}
	for _, n := range changedList {
		byFile[n.file] = append(byFile[n.file], n.name)
	}
	fmt.Fprintln(out, "\nchanged:")
	for _, f := range changedFiles {
		names := byFile[f]
		if len(names) == 0 {
			fmt.Fprintf(out, "  %s\n", f)
			continue
		}
		fmt.Fprintf(out, "  %s: %s\n", f, strings.Join(names, ", "))
	}
	if len(callers) > 0 {
		fmt.Fprintf(out, "\ncallers (%d, up to %d levels):\n", len(callers), depth)
		for _, c := range callers[:cap(len(callers))] {
			fmt.Fprintf(out, "  %-40s %s  [%s]\n", c.name, c.file, pathTo(c))
		}
		if len(callers) > cap(len(callers)) {
			fmt.Fprintf(out, "  … %d more (--limit=0 for all)\n", len(callers)-cap(len(callers)))
		}
	}
	if len(routes) > 0 {
		fmt.Fprintln(out, "\nAPI routes affected:")
		for _, r := range routes {
			fmt.Fprintf(out, "  %-36s via %s\n", r.label, r.path)
		}
	}
	if len(pages) > 0 {
		fmt.Fprintln(out, "\npages affected:")
		for _, p := range pages {
			fmt.Fprintf(out, "  %-36s via %s\n", p.label, p.path)
		}
	}
	if len(dbs) > 0 {
		fmt.Fprintln(out, "\ndatabase touched by the changed code:")
		var ks []string
		for k := range dbs {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			var ops []string
			for o := range dbs[k] {
				ops = append(ops, o)
			}
			sort.Strings(ops)
			fmt.Fprintf(out, "  %-36s %s\n", k, strings.Join(ops, ","))
		}
	}
	if len(calls) > 0 {
		sort.Strings(calls)
		fmt.Fprintln(out, "\nAPI routes the changed code calls:")
		for _, c := range calls {
			fmt.Fprintf(out, "  %s\n", c)
		}
	}
	if len(schema) > 0 {
		fmt.Fprintln(out, "\nschema declared by changed migrations (run `codumi audit`):")
		for _, s := range schema {
			fmt.Fprintf(out, "  %s\n", s)
		}
	}
	if len(testSet) > 0 {
		var ts []string
		for t := range testSet {
			ts = append(ts, t)
		}
		sort.Strings(ts)
		fmt.Fprintln(out, "\ntests on the path:")
		for _, t := range ts {
			fmt.Fprintf(out, "  %s\n", t)
		}
	} else if len(changedList) > 0 {
		fmt.Fprintln(out, "\nno test reaches the changed code")
	}
	return nil
}

// nonBlank tira das faixas as linhas em branco (uma linha vazia entre duas
// funções não é mudança em nenhuma delas).
func nonBlank(path string, spans []lineSpan) []lineSpan {
	b, err := os.ReadFile(path)
	if err != nil {
		return spans
	}
	lines := strings.Split(string(b), "\n")
	blank := func(n int) bool { return n >= 1 && n <= len(lines) && strings.TrimSpace(lines[n-1]) == "" }
	var outS []lineSpan
	for _, s := range spans {
		if s.from == 0 || s.to-s.from > 5000 {
			outS = append(outS, s)
			continue
		}
		from, to := s.from, s.to
		for from <= to && blank(from) {
			from++
		}
		for to >= from && blank(to) {
			to--
		}
		if from <= to {
			outS = append(outS, lineSpan{from, to})
		}
	}
	return outS
}
