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

	"github.com/codumihq/codumi/internal/store"
)

// ---- linked projects --------------------------------------------------------

// An index plus where it came from. Linked projects let `codumi route` show
// callers across repos (e.g. a TS app calling a Go API).
type index struct {
	name, root string
	db         *sql.DB
	local      bool
}

func linksFile(root string) string { return filepath.Join(root, store.Dir, "links") }

func readLinks(root string) []string {
	f, err := os.Open(linksFile(root))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func allIndexes(root string, local *sql.DB) []index {
	out := []index{{name: filepath.Base(root), root: root, db: local, local: true}}
	for _, l := range readLinks(root) {
		r, p, ok := store.Find(l)
		if !ok {
			fmt.Fprintf(notes, "note: linked project %s has no index; run `codumi index` there\n", l)
			continue
		}
		db, err := store.Open(p)
		if err != nil {
			continue
		}
		out = append(out, index{name: filepath.Base(r), root: r, db: db})
	}
	return out
}

func cmdLink(pos []string, flags map[string]string) error {
	cwd, _ := os.Getwd()
	root, _, ok := store.Find(cwd)
	if !ok {
		return errors.New("no index here; run `codumi index` first")
	}
	links := readLinks(root)
	if rm := flags["remove"]; rm != "" {
		abs, _ := filepath.Abs(rm)
		var kept []string
		for _, l := range links {
			if l != abs {
				kept = append(kept, l)
			}
		}
		links = kept
	} else if len(pos) > 0 {
		abs, err := filepath.Abs(pos[0])
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("%s does not exist", pos[0])
		}
		dup := false
		for _, l := range links {
			dup = dup || l == abs
		}
		if !dup {
			links = append(links, abs)
		}
		if _, _, ok := store.Find(abs); !ok {
			fmt.Fprintf(out, "note: %s has no index yet; run `codumi index` there\n", pos[0])
		}
	}
	if len(pos) > 0 || flags["remove"] != "" {
		if err := os.WriteFile(linksFile(root), []byte(strings.Join(links, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	if len(links) == 0 {
		fmt.Fprintln(out, "no linked projects. `codumi link ../other-repo` links one.")
		return nil
	}
	fmt.Fprintln(out, "linked projects:")
	for _, l := range links {
		fmt.Fprintf(out, "  %s\n", l)
	}
	return nil
}

// ---- route matching -----------------------------------------------------------

// normSeg maps route params of every style to * (one segment) or ** (rest).
func normSeg(s string) string {
	switch {
	case strings.HasPrefix(s, "[[...") || strings.HasPrefix(s, "[..."), strings.HasPrefix(s, "{") && strings.HasSuffix(s, "...}"), strings.HasPrefix(s, "*"):
		return "**"
	case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"), strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"), strings.HasPrefix(s, ":"):
		return "*"
	}
	return s
}

func segs(p string) []string {
	var out []string
	for _, s := range strings.Split(strings.Trim(p, "/"), "/") {
		if s != "" && s != "{$}" {
			out = append(out, s)
		}
	}
	return out
}

// matchRoute compares a declared route (/api/orders/[id], /v1/x/{id}) with a
// path seen in code (/api/orders/${} from a template string).
func matchRoute(route, call string) bool {
	rs, cs := segs(route), segs(call)
	for i, r := range rs {
		r = normSeg(r)
		if r == "**" {
			return true
		}
		if i >= len(cs) {
			return false
		}
		c := cs[i]
		if r == "*" || strings.Contains(c, "${}") {
			continue
		}
		if r != c {
			return false
		}
	}
	return len(cs) == len(rs)
}

func methodOK(route, call string) bool {
	return call == "" || route == "ANY" || route == "" || strings.EqualFold(route, call)
}

// ---- route --------------------------------------------------------------------

type routeRow struct {
	idx                          *index
	File, Method, Path, HFile, H string
	Line                         int
}

func findRoutes(ix *index, q string) []routeRow {
	rows, err := ix.db.Query(`SELECT file,method,path,line,handler_file,handler FROM routes ORDER BY path,method`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var exact, matched, partial []routeRow
	for rows.Next() {
		r := routeRow{idx: ix}
		rows.Scan(&r.File, &r.Method, &r.Path, &r.Line, &r.HFile, &r.H)
		switch {
		case strings.TrimSuffix(r.Path, "/") == q:
			exact = append(exact, r)
		case matchRoute(r.Path, q):
			matched = append(matched, r)
		case strings.Contains(r.Path, q):
			partial = append(partial, r)
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

type touch struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Ops   string `json:"ops"`
	At    string `json:"at"`
	Via   string `json:"via"`
	Admin bool   `json:"admin_client"`
}

type caller struct {
	Project string `json:"project"`
	At      string `json:"at"`
	From    string `json:"from"`
	Method  string `json:"method,omitempty"`
}

// reach walks the call graph from a handler (5 levels) collecting database uses.
func reach(db *sql.DB, file, handler string) []touch {
	type node struct{ file, name string }
	type item struct {
		n   node
		via []string
	}
	short := handler[strings.LastIndex(handler, ".")+1:]
	queue := []item{{node{file, handler}, []string{short}}}
	seen := map[node]bool{queue[0].n: true}
	var out []touch
	for depth := 0; depth < 6 && len(queue) > 0; depth++ {
		var next []item
		for _, it := range queue {
			if urows, err := db.Query(`SELECT type,name,ops,file,line,client FROM db_uses WHERE file=? AND from_name=?`, it.n.file, it.n.name); err == nil {
				for urows.Next() {
					var t touch
					var f, client string
					var l int
					urows.Scan(&t.Type, &t.Name, &t.Ops, &f, &l, &client)
					t.At, t.Via, t.Admin = fmt.Sprintf("%s:%d", f, l), strings.Join(it.via, " → "), bypassesRLS(client)
					out = append(out, t)
				}
				urows.Close()
			}
			erows, err := db.Query(`SELECT DISTINCT to_file,to_name FROM refs WHERE file=? AND from_name=? AND kind IN ('call','new','jsx','ref') AND to_file NOT LIKE 'ext:%'`, it.n.file, it.n.name)
			if err != nil {
				continue
			}
			for erows.Next() {
				var n node
				erows.Scan(&n.file, &n.name)
				if seen[n] {
					continue
				}
				seen[n] = true
				next = append(next, item{n, append(append([]string{}, it.via...), n.name)})
			}
			erows.Close()
		}
		queue = next
	}
	return out
}

func cmdRoute(pos []string, flags map[string]string) error {
	if len(pos) == 0 {
		return errors.New("usage: codumi route </api/path>  (exact path, a concrete URL path, or part of it)")
	}
	root, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	ixs := allIndexes(root, db)
	q := strings.TrimSuffix(strings.SplitN(pos[0], "?", 2)[0], "/")

	var routes []routeRow
	for i := range ixs {
		if rs := findRoutes(&ixs[i], q); len(rs) > 0 {
			routes = rs
			break
		}
	}
	if len(routes) == 0 {
		return fmt.Errorf("no route matches %q", pos[0])
	}
	paths := map[string]bool{}
	for _, r := range routes {
		paths[r.Path] = true
	}
	if len(paths) > 1 {
		fmt.Fprintf(out, "%d routes match %q:\n", len(paths), pos[0])
		seen := map[string]bool{}
		for _, r := range routes {
			if !seen[r.Path+r.Method] {
				seen[r.Path+r.Method] = true
				fmt.Fprintf(out, "  %-6s %-50s %s\n", r.Method, r.Path, r.File)
			}
		}
		return nil
	}

	// Every call site, in this project and the linked ones.
	type rc struct {
		ix               *index
		file, from, p, m string
		line             int
	}
	var calls []rc
	for i := range ixs {
		rows, err := ixs[i].db.Query(`SELECT file,line,from_name,path,method FROM route_calls`)
		if err != nil {
			continue
		}
		for rows.Next() {
			c := rc{ix: &ixs[i]}
			rows.Scan(&c.file, &c.line, &c.from, &c.p, &c.m)
			if matchRoute(routes[0].Path, c.p) {
				calls = append(calls, c)
			}
		}
		rows.Close()
	}

	type res struct {
		Project string   `json:"project"`
		Method  string   `json:"method"`
		Path    string   `json:"path"`
		At      string   `json:"at"`
		Handler string   `json:"handler"`
		Touches []touch  `json:"touches"`
		Callers []caller `json:"callers"`
	}
	var results []res
	for _, r := range routes {
		rr := res{Project: r.idx.name, Method: r.Method, Path: r.Path, At: fmt.Sprintf("%s:%d", r.File, r.Line), Handler: r.H}
		if r.HFile != "" && r.H != "" {
			rr.Touches = reach(r.idx.db, r.HFile, r.H)
		}
		for _, c := range calls {
			if methodOK(r.Method, c.m) {
				rr.Callers = append(rr.Callers, caller{Project: c.ix.name, At: fmt.Sprintf("%s:%d", c.file, c.line), From: c.from, Method: c.m})
			}
		}
		results = append(results, rr)
	}
	if flags["json"] == "true" {
		return printJSON(results)
	}
	multi := len(ixs) > 1
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(out)
		}
		where := r.At
		if multi {
			where = r.Project + ": " + where
		}
		fmt.Fprintf(out, "%s %s  %s\n", r.Method, r.Path, where)
		if r.Handler != "" && r.Handler != r.Method {
			fmt.Fprintf(out, "handler: %s\n", r.Handler)
		}
		printTouches(r.Touches, flags["detail"] == "true")
		fmt.Fprintf(out, "called from: %d %s\n", len(r.Callers), plural(len(r.Callers), "place", "places"))
		for _, c := range r.Callers {
			at := c.At
			if multi {
				at = c.Project + ": " + at
			}
			fmt.Fprintf(out, "  %s  %s\n", at, c.From)
		}
	}
	return nil
}

func printTouches(ts []touch, detail bool) {
	if len(ts) == 0 {
		fmt.Fprintln(out, "database: nothing found through its calls")
		return
	}
	// One entry per table/rpc/bucket: union of ops, use count and its sites.
	type agg struct {
		typ, name string
		ops       map[string]bool
		sites     []touch
		admin     bool
	}
	var order []string
	m := map[string]*agg{}
	for _, t := range ts {
		k := t.Type + ":" + t.Name
		a := m[k]
		if a == nil {
			a = &agg{typ: t.Type, name: t.Name, ops: map[string]bool{}}
			m[k] = a
			order = append(order, k)
		}
		a.sites = append(a.sites, t)
		a.admin = a.admin || t.Admin
		for _, o := range strings.Split(t.Ops, ",") {
			if o != "" {
				a.ops[o] = true
			}
		}
	}
	sort.Strings(order)
	hint := "--detail for sites and call paths"
	if detail {
		hint = "up to 3 sites each; --json for every line"
	}
	fmt.Fprintf(out, "database: %d %s across %d %s (%s)\n", len(order), plural(len(order), "object", "objects"), len(ts), plural(len(ts), "use", "uses"), hint)
	for _, k := range order {
		a := m[k]
		var ops []string
		for o := range a.ops {
			ops = append(ops, o)
		}
		sort.Strings(ops)
		admin := ""
		if a.admin {
			admin = "  [admin]"
		}
		fmt.Fprintf(out, "  %-6s %-32s %-28s ×%d%s\n", a.typ, a.name, strings.Join(ops, ","), len(a.sites), admin)
		if !detail {
			continue
		}
		// Shortest call paths first: they are the most direct way in.
		sites := append([]touch{}, a.sites...)
		sort.SliceStable(sites, func(i, j int) bool {
			return strings.Count(sites[i].Via, "→") < strings.Count(sites[j].Via, "→")
		})
		for i, t := range sites {
			if i == 3 {
				fmt.Fprintf(out, "         … %d more\n", len(sites)-3)
				break
			}
			fmt.Fprintf(out, "         %s  via %s\n", t.At, shortVia(t.Via))
		}
	}
}

// shortVia keeps the last three hops of a call path: the part that explains
// how the code reaches the database.
func shortVia(v string) string {
	parts := strings.Split(v, " → ")
	if len(parts) <= 3 {
		return v
	}
	return "… → " + strings.Join(parts[len(parts)-3:], " → ")
}
