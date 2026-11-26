package cli

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---- codumi page ------------------------------------------------------------------

type pageRow struct {
	File, Path, Component string
	Line                  int
}

type treeNode struct {
	File     string      `json:"file"`
	Name     string      `json:"name"`
	Line     int         `json:"line"`
	Children []*treeNode `json:"children,omitempty"`
	Repeat   bool        `json:"repeat,omitempty"`
}

// componentAsPage acha um componente de tela pelo nome quando a rota não está
// no índice (ex.: rotas montadas à mão): "inscricoes" → src/pages/Inscricoes.tsx.
func componentAsPage(db *sql.DB, q string) []pageRow {
	seg := strings.ToLower(strings.Trim(q, "/"))
	if i := strings.LastIndexByte(seg, '/'); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.NewReplacer("-", "", "_", "").Replace(seg)
	if seg == "" {
		return nil
	}
	rows, err := db.Query(`SELECT file, name, line FROM decls WHERE instr(name, '.') = 0 AND kind IN ('function','component','class','const')
		AND (file LIKE '%.tsx' OR file LIKE '%.jsx' OR file LIKE '%.svelte' OR file LIKE '%.astro' OR file LIKE '%.vue' OR file LIKE '%.dart')`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var exact, starts []pageRow
	for rows.Next() {
		var p pageRow
		rows.Scan(&p.File, &p.Component, &p.Line)
		n := strings.ToLower(p.Component)
		if n == "" || n[0] < 'a' || n[0] > 'z' || strings.ToUpper(p.Component[:1]) != p.Component[:1] {
			continue // componente começa com maiúscula
		}
		p.Path = "(component)"
		inPages := strings.Contains(p.File, "/pages/") || strings.Contains(p.File, "/screens/") || strings.Contains(p.File, "/views/") || strings.Contains(p.File, "/routes/")
		switch {
		case n == seg || n == seg+"page" || n == seg+"screen" || n == seg+"view":
			if inPages {
				exact = append([]pageRow{p}, exact...)
			} else {
				exact = append(exact, p)
			}
		case strings.HasPrefix(n, seg) && inPages:
			starts = append(starts, p)
		}
	}
	if len(exact) > 0 {
		return exact[:1]
	}
	if len(starts) > 8 {
		starts = starts[:8]
	}
	return starts
}

func findPages(db *sql.DB, q string) []pageRow {
	rows, err := db.Query(`SELECT file,path,line,component FROM pages ORDER BY path`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var exact, matched, partial []pageRow
	for rows.Next() {
		var p pageRow
		rows.Scan(&p.File, &p.Path, &p.Line, &p.Component)
		switch {
		case p.Path == q || (q == "/" && p.Path == "/"):
			exact = append(exact, p)
		case strings.HasPrefix(q, "/") && matchRoute(p.Path, q):
			// só um caminho de verdade casa com /:param e /* (uma palavra solta, não)
			matched = append(matched, p)
		case strings.Contains(strings.ToLower(p.Path), strings.ToLower(q)) || strings.Contains(strings.ToLower(p.Component), strings.ToLower(strings.Trim(q, "/"))):
			partial = append(partial, p)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	if len(matched) > 0 {
		return matched
	}
	return partial
}

func declLine(db *sql.DB, file, name string) int {
	var l int
	db.QueryRow(`SELECT line FROM decls WHERE file=? AND name=?`, file, name).Scan(&l)
	return l
}

// componentTree follows JSX uses from a component, depth-first, in the
// order they appear. A component already shown is marked instead of
// expanded again.
func componentTree(db *sql.DB, file, name string, depth int) *treeNode {
	t, _ := componentTreeOpts(db, file, name, depth, true)
	return t
}

// isPrimitive: design-system building blocks (components/ui/…, shadcn-style)
// that every screen uses; listing them in the tree is noise.
func isPrimitive(file string) bool {
	return strings.Contains(file, "/components/ui/") || strings.HasPrefix(file, "components/ui/") || strings.Contains(file, "/ui/primitives/")
}

// componentTreeOpts: with compact, primitives are collected apart and
// components already shown are left out.
func componentTreeOpts(db *sql.DB, file, name string, depth int, compact bool) (*treeNode, []string) {
	prim := map[string]bool{}
	seen := map[string]bool{}
	var walk func(f, n string, d int) *treeNode
	walk = func(f, n string, d int) *treeNode {
		node := &treeNode{File: f, Name: n, Line: declLine(db, f, n)}
		key := f + "\x00" + n
		if seen[key] {
			node.Repeat = true
			return node
		}
		seen[key] = true
		if d >= depth {
			return node
		}
		rows, err := db.Query(`SELECT to_file, to_name, MIN(line) FROM refs WHERE file=? AND from_name=? AND kind='jsx' GROUP BY to_file, to_name ORDER BY MIN(line)`, f, n)
		if err != nil {
			return node
		}
		type kid struct {
			f, n string
		}
		var kids []kid
		for rows.Next() {
			var k kid
			var l int
			rows.Scan(&k.f, &k.n, &l)
			kids = append(kids, k)
		}
		rows.Close()
		for _, k := range kids {
			if compact && isPrimitive(k.f) {
				prim[k.n] = true
				continue
			}
			c := walk(k.f, k.n, d+1)
			if compact && c.Repeat {
				continue
			}
			node.Children = append(node.Children, c)
		}
		return node
	}
	t := walk(file, name, 0)
	var ps []string
	for p := range prim {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return t, ps
}

// layoutsFor lists the layout files that wrap an app-router page.
func layoutsFor(db *sql.DB, pageFile string) []pageRow {
	i := strings.Index(pageFile, "app/")
	if i < 0 {
		return nil
	}
	base := pageFile[:i+3] // ".../app"
	dir := filepath.ToSlash(filepath.Dir(pageFile))
	var dirs []string
	for {
		dirs = append([]string{dir}, dirs...)
		if dir == base || !strings.HasPrefix(dir, base) {
			break
		}
		dir = filepath.ToSlash(filepath.Dir(dir))
	}
	var out []pageRow
	for _, d := range dirs {
		for _, ext := range []string{"tsx", "jsx", "ts", "js"} {
			f := d + "/layout." + ext
			var n string
			var l int
			// the layout component: an exported function/class, not metadata constants
			if db.QueryRow(`SELECT name, line FROM decls WHERE file=? AND exported=1 ORDER BY CASE WHEN kind IN ('function','class') THEN 0 ELSE 1 END, line LIMIT 1`, f).Scan(&n, &l) == nil {
				out = append(out, pageRow{File: f, Component: n, Line: l})
				break
			}
		}
	}
	return out
}

func treeNodes(n *treeNode, acc map[[2]string]bool) {
	acc[[2]string{n.File, n.Name}] = true
	for _, c := range n.Children {
		treeNodes(c, acc)
	}
}

func cmdPage(pos []string, flags map[string]string) error {
	if len(pos) == 0 {
		return errors.New("usage: codumi page </path>  (a page route like / or /orders/[id], or part of it)")
	}
	_, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	q := strings.TrimSpace(strings.SplitN(pos[0], "?", 2)[0])
	if q != "/" {
		q = strings.TrimSuffix(q, "/")
	}
	pages := findPages(db, q)
	if len(pages) == 0 {
		// sem rota com esse nome: tenta o componente da tela (Inscricoes, InscricoesPage…)
		pages = componentAsPage(db, q)
		if len(pages) == 0 {
			return fmt.Errorf("no page or screen component matches %q. Try codumi_find with a text from the screen, or codumi_refs with the component name", pos[0])
		}
	}
	if len(pages) > 1 {
		fmt.Fprintf(out, "%d pages match %q:\n", len(pages), pos[0])
		for _, p := range pages {
			fmt.Fprintf(out, "  %-40s %s  %s\n", p.Path, p.Component, p.File)
		}
		return nil
	}
	p := pages[0]
	depth := intFlag(flags, "depth", 3)
	tree, prims := componentTreeOpts(db, p.File, p.Component, depth, flags["all"] != "true")
	layouts := layoutsFor(db, p.File)

	// What the whole tree touches: database and API calls.
	nodes := map[[2]string]bool{}
	treeNodes(tree, nodes)
	for _, l := range layouts {
		nodes[[2]string{l.File, l.Component}] = true
	}
	type use struct {
		Type, Name string
		Ops        map[string]bool
		N          int
	}
	dbu := map[string]*use{}
	var calls []string
	seenCall := map[string]bool{}
	for k := range nodes {
		if rows, err := db.Query(`SELECT type,name,ops FROM db_uses WHERE file=? AND from_name=?`, k[0], k[1]); err == nil {
			for rows.Next() {
				var typ, name, ops string
				rows.Scan(&typ, &name, &ops)
				u := dbu[typ+" "+name]
				if u == nil {
					u = &use{Type: typ, Name: name, Ops: map[string]bool{}}
					dbu[typ+" "+name] = u
				}
				u.N++
				for _, o := range strings.Split(ops, ",") {
					if o != "" {
						u.Ops[o] = true
					}
				}
			}
			rows.Close()
		}
		if rows, err := db.Query(`SELECT method,path FROM route_calls WHERE file=? AND from_name=?`, k[0], k[1]); err == nil {
			for rows.Next() {
				var m, pth string
				rows.Scan(&m, &pth)
				c := strings.TrimSpace(m + " " + pth)
				if !seenCall[c] {
					seenCall[c] = true
					calls = append(calls, c)
				}
			}
			rows.Close()
		}
	}
	sort.Strings(calls)

	if flags["json"] == "true" {
		var dbl []map[string]any
		for _, u := range dbu {
			var ops []string
			for o := range u.Ops {
				ops = append(ops, o)
			}
			sort.Strings(ops)
			dbl = append(dbl, map[string]any{"type": u.Type, "name": u.Name, "ops": ops, "uses": u.N})
		}
		return printJSON(map[string]any{"path": p.Path, "file": p.File, "line": p.Line, "component": p.Component, "layouts": layouts, "tree": tree, "ui_primitives": prims, "database": dbl, "api_calls": calls})
	}
	fmt.Fprintf(out, "page %s  %s:%d  %s\n", p.Path, p.File, p.Line, p.Component)
	if len(layouts) > 0 {
		var ls []string
		for _, l := range layouts {
			ls = append(ls, fmt.Sprintf("%s (%s)", l.File, l.Component))
		}
		fmt.Fprintf(out, "layouts: %s\n", strings.Join(ls, " → "))
	}
	fmt.Fprintf(out, "components (JSX, %d levels; --depth=N for more):\n", depth)
	var draw func(n *treeNode, prefix string, last bool, root bool)
	draw = func(n *treeNode, prefix string, last bool, root bool) {
		branch, next := "", prefix
		if !root {
			if last {
				branch, next = prefix+"└─ ", prefix+"   "
			} else {
				branch, next = prefix+"├─ ", prefix+"│  "
			}
		}
		where := n.File
		if n.Line > 0 {
			where = fmt.Sprintf("%s:%d", n.File, n.Line)
		}
		mark := ""
		if n.Repeat {
			mark = "  (shown above)"
		}
		fmt.Fprintf(out, "  %s%s  %s%s\n", branch, n.Name, where, mark)
		for i, c := range n.Children {
			draw(c, next, i == len(n.Children)-1, false)
		}
	}
	draw(tree, "", true, true)
	if len(prims) > 0 {
		fmt.Fprintf(out, "ui: %s  (--all to expand)\n", strings.Join(prims, ", "))
	}
	if len(dbu) > 0 {
		var keys []string
		for k := range dbu {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(out, "database: %d %s\n", len(keys), plural(len(keys), "object", "objects"))
		for _, k := range keys {
			u := dbu[k]
			var ops []string
			for o := range u.Ops {
				ops = append(ops, o)
			}
			sort.Strings(ops)
			fmt.Fprintf(out, "  %-6s %-30s %s\n", u.Type, u.Name, strings.Join(ops, ","))
		}
	}
	if len(calls) > 0 {
		fmt.Fprintf(out, "api calls: %s\n", strings.Join(calls, ", "))
	}
	return nil
}

// ---- codumi find ---------------------------------------------------------------------

type findHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	In   string `json:"in"`
	Text string `json:"text"`
}

// enclosing returns the declaration a line belongs to: the closest one that
// starts at or before it (top-level or Class.member).
func enclosing(decls []declRow, line int) string {
	best := ""
	for _, d := range decls {
		if d.Line <= line {
			best = d.Name
		} else {
			break
		}
	}
	return best
}

func cmdFind(pos []string, flags map[string]string) error {
	if len(pos) == 0 || strings.TrimSpace(strings.Join(pos, " ")) == "" {
		return errors.New(`usage: codumi find "<text>"   (case-insensitive, in the project's indexed source files)`)
	}
	root, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	needle := strings.Join(pos, " ")
	low := strings.ToLower(needle)
	limit := intFlag(flags, "limit", 40)
	files := indexedFiles(db)
	// código primeiro; dados (json, csv, md, sql, lock) por último
	isData := func(f string) bool {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".json", ".csv", ".md", ".txt", ".sql", ".lock", ".yaml", ".yml", ".svg", ".xml", ".html":
			return true
		}
		return false
	}
	sort.Slice(files, func(i, j int) bool {
		if isData(files[i]) != isData(files[j]) {
			return !isData(files[i])
		}
		return files[i] < files[j]
	})
	perFile := map[string]int{}
	var hits []findHit
	total, nfiles := 0, 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || !strings.Contains(strings.ToLower(string(b)), low) {
			continue
		}
		var decls []declRow
		if rows, err := db.Query(`SELECT name,line FROM decls WHERE file=? ORDER BY line`, f); err == nil {
			for rows.Next() {
				var d declRow
				rows.Scan(&d.Name, &d.Line)
				decls = append(decls, d)
			}
			rows.Close()
		}
		nfiles++
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		n := 0
		for sc.Scan() {
			n++
			line := sc.Text()
			i := strings.Index(strings.ToLower(line), low)
			if i < 0 {
				continue
			}
			total++
			perFile[f]++
			if limit > 0 && len(hits) >= limit {
				continue
			}
			text := strings.TrimSpace(line)
			if len(text) > 160 {
				// keep the match in view
				start := strings.Index(strings.ToLower(text), low) - 60
				if start < 0 {
					start = 0
				}
				end := start + 160
				if end > len(text) {
					end = len(text)
				}
				text = "…" + text[start:end] + "…"
			}
			hits = append(hits, findHit{File: f, Line: n, In: enclosing(decls, n), Text: text})
		}
	}
	if flags["json"] == "true" {
		return printJSON(map[string]any{"text": needle, "total": total, "files": nfiles, "hits": hits})
	}
	if total == 0 {
		fmt.Fprintf(out, "%q appears in no indexed source file\n", needle)
		return nil
	}
	fmt.Fprintf(out, "%q: %d %s in %d %s\n", needle, total, plural(total, "match", "matches"), nfiles, plural(nfiles, "file", "files"))
	last := ""
	for _, h := range hits {
		if h.File != last {
			fmt.Fprintln(out, h.File)
			last = h.File
		}
		in := h.In
		if in == "" {
			in = "<module>"
		}
		fmt.Fprintf(out, "  %-5d %-24s %s\n", h.Line, in, h.Text)
	}
	if total > len(hits) {
		fmt.Fprintf(out, "… %d more not listed. Narrow the text, or use codumi_page / codumi_refs.\n", total-len(hits))
		// onde se concentram os outros: os arquivos com mais ocorrências
		type fc struct {
			f string
			n int
		}
		var top []fc
		for f, n := range perFile {
			top = append(top, fc{f, n})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n || top[i].n == top[j].n && top[i].f < top[j].f })
		if len(top) > 15 {
			top = top[:15]
		}
		fmt.Fprintln(out, "files with most matches:")
		for _, x := range top {
			fmt.Fprintf(out, "  %4d  %s\n", x.n, x.f)
		}
	}
	return nil
}
