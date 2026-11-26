package cli

// codumi overview: o retrato do projeto tirado do índice, para quem chega
// (ou para um agente escrever a explicação): pastas e linguagens, telas,
// rotas de API, tabelas do banco e quem mais usa cada uma, e as peças mais
// chamadas do código.

import (
	"database/sql"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Overview monta o retrato em texto (Markdown simples).
func Overview(root string) (string, error) {
	db, err := openMap(root)
	if err != nil {
		return "", fmt.Errorf("no Codumi index here yet: run codumi index")
	}
	defer db.Close()
	var b strings.Builder
	q := func(query string, args ...any) *sql.Rows {
		r, err := db.Query(query, args...)
		if err != nil {
			return nil
		}
		return r
	}

	// pastas e linguagens
	type dir struct {
		name         string
		files, lines int
		langs        map[string]int
	}
	dirs := map[string]*dir{}
	langs := map[string]int{}
	totalFiles, totalLines := 0, 0
	if r := q(`SELECT f.path, f.lines, COALESCE(s.lang,'') FROM files f LEFT JOIN file_state s ON s.path = f.path`); r != nil {
		for r.Next() {
			var p, lang string
			var n int
			r.Scan(&p, &n, &lang)
			parts := strings.Split(p, "/")
			key := parts[0]
			if len(parts) > 2 && (key == "src" || key == "app" || key == "lib" || key == "internal" || key == "packages" || key == "apps") {
				key = parts[0] + "/" + parts[1]
			} else if len(parts) == 1 {
				key = "(raiz)"
			}
			d := dirs[key]
			if d == nil {
				d = &dir{name: key, langs: map[string]int{}}
				dirs[key] = d
			}
			d.files++
			d.lines += n
			if lang == "" {
				lang = strings.TrimPrefix(path.Ext(p), ".")
			}
			if lang != "" {
				d.langs[lang]++
				langs[lang]++
			}
			totalFiles++
			totalLines += n
		}
		r.Close()
	}
	top := func(m map[string]int, n int) string {
		type kv struct {
			k string
			v int
		}
		var xs []kv
		for k, v := range m {
			xs = append(xs, kv{k, v})
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].v > xs[j].v })
		var out []string
		for i, x := range xs {
			if i >= n {
				break
			}
			out = append(out, fmt.Sprintf("%s %d", x.k, x.v))
		}
		return strings.Join(out, ", ")
	}
	fmt.Fprintf(&b, "# Retrato do projeto\n\n%d arquivos indexados, %d linhas. Linguagens: %s.\n\n## Pastas\n", totalFiles, totalLines, top(langs, 6))
	var ds []*dir
	for _, d := range dirs {
		ds = append(ds, d)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].lines > ds[j].lines })
	for i, d := range ds {
		if i >= 25 {
			fmt.Fprintf(&b, "- … %d pastas a mais\n", len(ds)-25)
			break
		}
		fmt.Fprintf(&b, "- %s: %d arquivos, %d linhas (%s)\n", d.name, d.files, d.lines, top(d.langs, 3))
	}

	// telas
	if r := q(`SELECT DISTINCT path, file, COALESCE(component,'') FROM pages ORDER BY path LIMIT 80`); r != nil {
		var ls []string
		for r.Next() {
			var p, f, c string
			r.Scan(&p, &f, &c)
			ls = append(ls, fmt.Sprintf("- %s → %s", p, f))
		}
		r.Close()
		if len(ls) > 0 {
			b.WriteString("\n## Telas\n" + strings.Join(ls, "\n") + "\n")
		}
	}
	// rotas de API
	if r := q(`SELECT method, path, file, COALESCE(guards,'') FROM routes ORDER BY path LIMIT 100`); r != nil {
		var ls []string
		for r.Next() {
			var m, p, f, g string
			r.Scan(&m, &p, &f, &g)
			line := fmt.Sprintf("- %s %s → %s", m, p, f)
			if g != "" {
				line += " (proteção: " + g + ")"
			}
			ls = append(ls, line)
		}
		r.Close()
		if len(ls) > 0 {
			b.WriteString("\n## Rotas de API\n" + strings.Join(ls, "\n") + "\n")
		}
	}
	// banco: tabelas mais usadas pelo código
	if r := q(`SELECT name, type, COUNT(*) n, COUNT(DISTINCT file) nf, GROUP_CONCAT(DISTINCT ops) FROM db_uses GROUP BY name, type ORDER BY n DESC LIMIT 40`); r != nil {
		var ls []string
		for r.Next() {
			var name, typ, ops string
			var n, nf int
			r.Scan(&name, &typ, &n, &nf, &ops)
			seen := map[string]bool{}
			var uniq []string
			for _, o := range strings.Split(ops, ",") {
				if o = strings.TrimSpace(o); o != "" && !seen[o] {
					seen[o] = true
					uniq = append(uniq, o)
				}
			}
			ops = strings.Join(uniq, ",")
			ls = append(ls, fmt.Sprintf("- %s (%s): %d usos em %d arquivos [%s]", name, typ, n, nf, ops))
		}
		r.Close()
		if len(ls) > 0 {
			b.WriteString("\n## Banco de dados (pelo uso no código)\n" + strings.Join(ls, "\n") + "\n")
		}
	}
	var nt, nf int
	db.QueryRow(`SELECT COUNT(*) FROM sql_tables`).Scan(&nt)
	db.QueryRow(`SELECT COUNT(*) FROM sql_functions`).Scan(&nf)
	if nt+nf > 0 {
		fmt.Fprintf(&b, "\nNas migrações: %d tabelas/views e %d funções SQL.\n", nt, nf)
	}
	// as peças mais chamadas
	if r := q(`SELECT to_name, to_file, COUNT(*) n, COUNT(DISTINCT file) nf FROM refs WHERE kind != 'import' AND to_name != '' GROUP BY to_name, to_file ORDER BY nf DESC, n DESC LIMIT 25`); r != nil {
		var ls []string
		for r.Next() {
			var name, file string
			var n, nf int
			r.Scan(&name, &file, &n, &nf)
			ls = append(ls, fmt.Sprintf("- %s (%s): usado em %d arquivos", name, file, nf))
		}
		r.Close()
		if len(ls) > 0 {
			b.WriteString("\n## O que mais é usado no código\n" + strings.Join(ls, "\n") + "\n")
		}
	}
	// fichas que já existem
	if ns := ListNotes(root); len(ns) > 0 {
		b.WriteString("\n## Fichas dos módulos\n")
		for _, n := range ns {
			fmt.Fprintf(&b, "- %s: %s\n", n.Name, n.Description)
		}
	}
	return b.String(), nil
}

func cmdOverview(pos []string, flags map[string]string) error {
	root, db, err := openIndex()
	if err != nil {
		return err
	}
	db.Close()
	s, err := Overview(root)
	if err != nil {
		return err
	}
	fmt.Fprint(out, s)
	return nil
}
