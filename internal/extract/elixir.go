package extract

// Elixir / Phoenix sem compilador: módulos (defmodule A.B), funções como
// Modulo.funcao (def/defp/defmacro, uma vez por nome), escopo por do/fn/end.
// Os usos seguem alias (inclusive alias A.{B, C} e as:), import, Modulo.f,
// chamadas locais e %Struct{}. Do Phoenix: o router vira rotas (scope,
// get/post…, resources, live) e as telas LiveView viram telas. Do Ecto:
// schema "tabela" liga o módulo à tabela, e Repo.all/insert/update/delete
// e from(x in Schema) viram uso de tabela.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

var exSkip = map[string]bool{"_build": true, "deps": true, "node_modules": true, "cover": true, "priv": true}

type exFile struct {
	rel   string
	toks  []tok
	lines int
}

type exDecl struct {
	file, name, kind string
	line             int
}

type exScope struct {
	kind byte // 'm' módulo, 'f' função, 'b' bloco
	name string
}

type exCtx struct {
	stack   []exScope
	pending exScope
	inline  string // def x, do: … (função de uma linha)
}

func (c *exCtx) mod() string {
	for k := len(c.stack) - 1; k >= 0; k-- {
		if c.stack[k].kind == 'm' {
			return c.stack[k].name
		}
	}
	return ""
}

func (c *exCtx) from() string {
	if c.pending.kind == 'f' {
		return c.pending.name
	}
	if c.inline != "" {
		return c.inline
	}
	for k := len(c.stack) - 1; k >= 0; k-- {
		if c.stack[k].kind != 'b' {
			return c.stack[k].name
		}
	}
	return "<module>"
}

// exMod lê A.B.C a partir de i.
func exMod(t []tok, i int) (string, int) {
	parts := []string{t[i].s}
	j := i
	for j+2 < len(t) && t[j+1].s == "." && t[j+2].k == 'i' && isUpper(t[j+2].s) {
		parts = append(parts, t[j+2].s)
		j += 2
	}
	return strings.Join(parts, "."), j
}

var exDefs = map[string]string{"def": "function", "defp": "function", "defmacro": "macro", "defmacrop": "macro", "defdelegate": "function", "defguard": "function"}

func (c *exCtx) step(t []tok, i int) (name, kind string, skip int) {
	tk := t[i]
	if tk.k != 'i' || i > 0 && t[i-1].s == "." {
		return "", "", 0
	}
	kw := i+1 < len(t) && t[i+1].s == ":" && t[i+1].line == tk.line
	switch tk.s {
	case "do":
		if kw {
			if c.pending.kind == 'f' {
				c.inline = c.pending.name
			}
			c.pending = exScope{}
			return "", "", 0
		}
		c.inline = ""
		s := c.pending
		if s.kind == 0 {
			s.kind = 'b'
		}
		c.stack = append(c.stack, s)
		c.pending = exScope{}
	case "fn":
		c.stack = append(c.stack, exScope{kind: 'b'})
	case "end":
		c.inline = ""
		if len(c.stack) > 0 {
			c.stack = c.stack[:len(c.stack)-1]
		}
	case "defmodule":
		if i+1 < len(t) && t[i+1].k == 'i' {
			n, j := exMod(t, i+1)
			if m := c.mod(); m != "" {
				n = m + "." + n
			}
			c.pending = exScope{kind: 'm', name: n}
			return n, "module", j - i
		}
	default:
		if k, ok := exDefs[tk.s]; ok && !kw && i+1 < len(t) && t[i+1].k == 'i' {
			c.inline = ""
			full := t[i+1].s
			if m := c.mod(); m != "" {
				full = m + "." + full
			}
			c.pending = exScope{kind: 'f', name: full}
			return full, k, 1
		}
	}
	return "", "", 0
}

// Elixir extrai o código Elixir. Com only, só esses arquivos entram no grafo.
func Elixir(root string, g *model.Graph, only []string) error {
	var files []*exFile
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || exSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".ex") && !strings.HasSuffix(p, ".exs") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		files = append(files, &exFile{rel: filepath.ToSlash(rel), toks: lexHash(src, hashElixir), lines: strings.Count(string(src), "\n") + 1})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	decls := map[string]*exDecl{}
	schemas := map[string]string{} // módulo → tabela
	all := make([][]*exDecl, len(files))
	for fi, f := range files {
		c := &exCtx{}
		t := f.toks
		for i := 0; i < len(t); i++ {
			name, kind, skip := c.step(t, i)
			if name != "" {
				if _, dup := decls[name]; !dup {
					d := &exDecl{file: f.rel, name: name, kind: kind, line: t[i].line}
					decls[name] = d
					all[fi] = append(all[fi], d)
				}
			}
			if t[i].s == "schema" && i+1 < len(t) && t[i+1].k == 's' && (i == 0 || t[i-1].s != ".") {
				schemas[c.mod()] = t[i+1].s
			}
			i += skip
		}
	}
	want := map[string]bool{}
	for _, o := range only {
		want[o] = true
	}
	for fi, f := range files {
		if only != nil && !want[f.rel] {
			continue
		}
		g.Files = append(g.Files, model.File{Path: f.rel, Lines: f.lines})
		for _, d := range all[fi] {
			g.Decls = append(g.Decls, model.Decl{File: d.file, Name: d.name, Kind: d.kind, Line: d.line, Exported: true})
		}
		f.uses(decls, schemas, g)
	}
	return nil
}

var repoOps = map[string]string{"all": "select", "get": "select", "get!": "select", "get_by": "select", "get_by!": "select", "one": "select", "one!": "select", "exists?": "select", "aggregate": "select", "stream": "select", "preload": "select", "reload": "select",
	"insert": "insert", "insert!": "insert", "insert_all": "insert", "insert_or_update": "upsert", "insert_or_update!": "upsert",
	"update": "update", "update!": "update", "update_all": "update", "delete": "delete", "delete!": "delete", "delete_all": "delete"}

func (f *exFile) uses(decls map[string]*exDecl, schemas map[string]string, g *model.Graph) {
	t := f.toks
	c := &exCtx{}
	alias := map[string]string{}
	var imports []string
	ref := func(line int, d *exDecl, kind string) {
		g.Refs = append(g.Refs, model.Ref{File: f.rel, Line: line, From: c.from(), ToFile: d.file, To: d.name, Kind: kind})
	}
	expand := func(m string) string {
		if m == "__MODULE__" {
			return c.mod()
		}
		first, rest, _ := strings.Cut(m, ".")
		if a, ok := alias[first]; ok {
			if rest != "" {
				return a + "." + rest
			}
			return a
		}
		// módulo irmão dentro do mesmo pai
		if cur := c.mod(); cur != "" && decls[m] == nil {
			if k := strings.LastIndexByte(cur, '.'); k > 0 && decls[cur[:k]+"."+m] != nil {
				return cur[:k] + "." + m
			}
		}
		return m
	}
	isRouter := false
	for _, tk := range t {
		if tk.s == "Phoenix" || tk.s == "router" {
			isRouter = true
			break
		}
	}
	isRouter = isRouter && strings.HasSuffix(f.rel, "router.ex")
	for i := 0; i < len(t); i++ {
		tk := t[i]
		name, _, skip := c.step(t, i)
		if name != "" {
			i += skip
			continue
		}
		switch tk.k {
		case 's':
			if i > 0 && t[i-1].s == "(" {
				if p, ok := apiRoute(tk.s); ok {
					g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: f.rel, Line: tk.line, From: c.from(), Path: p})
				}
			}
			if looksSQL.MatchString(tk.s) {
				sqlUse(g, f.rel, tk.line, c.from(), tk.s, "")
			}
			continue
		case 'i':
		default:
			continue
		}
		if i > 0 && t[i-1].s == "." && !(i > 1 && t[i-2].s == "__MODULE__") {
			continue
		}
		switch tk.s {
		case "alias":
			if i+1 < len(t) && t[i+1].k == 'i' {
				base, j := exMod(t, i+1)
				if j+2 < len(t) && t[j+1].s == "." && t[j+2].s == "{" {
					for k := j + 3; k < len(t) && t[k].s != "}"; k++ {
						if t[k].k == 'i' && isUpper(t[k].s) && (t[k-1].s == "{" || t[k-1].s == ",") {
							sub, e := exMod(t, k)
							alias[sub[strings.LastIndexByte(sub, '.')+1:]] = base + "." + sub
							k = e
						}
					}
				} else {
					short := base[strings.LastIndexByte(base, '.')+1:]
					if j+4 < len(t) && t[j+1].s == "," && t[j+2].s == "as" && t[j+3].s == ":" {
						short = t[j+4].s
					}
					alias[short] = base
				}
				if d := decls[base]; d != nil {
					ref(tk.line, d, "import")
				}
				i = j
				continue
			}
		case "import":
			if i+1 < len(t) && t[i+1].k == 'i' && isUpper(t[i+1].s) {
				m, j := exMod(t, i+1)
				m = expand(m)
				imports = append(imports, m)
				if d := decls[m]; d != nil {
					ref(tk.line, d, "import")
				}
				i = j
				continue
			}
		case "from":
			// from(p in Pedido, …) / from p in Pedido
			for k := i + 1; k+1 < len(t) && k < i+6; k++ {
				if t[k].s == "in" && t[k+1].k == 'i' && isUpper(t[k+1].s) {
					m, _ := exMod(t, k+1)
					if tb := schemas[expand(m)]; tb != "" {
						g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: tk.line, From: c.from(), Type: "table", Name: tb, Ops: []string{"select"}, Client: "Ecto"})
					}
					break
				}
			}
		}
		if isRouter {
			continue // o router é lido à parte
		}
		if isUpper(tk.s) || tk.s == "__MODULE__" {
			m, j := exMod(t, i)
			full := expand(m)
			kind := "ref"
			if i > 0 && t[i-1].s == "%" {
				kind = "new"
			}
			if j+2 < len(t) && t[j+1].s == "." && t[j+2].k == 'i' && !isUpper(t[j+2].s) {
				fn := t[j+2].s
				if d := decls[full+"."+fn]; d != nil {
					ref(t[j+2].line, d, "call")
				} else if d := decls[full]; d != nil {
					ref(tk.line, d, kind)
				}
				// Repo.all(Pedido) / %Pedido{} |> Repo.insert()
				if strings.HasSuffix(full, "Repo") {
					if op := repoOps[fn]; op != "" {
						tb := ""
						if e := matchParen(t, j+3); j+3 < len(t) && t[j+3].s == "(" && e > 0 {
							for k := j + 4; k < e && tb == ""; k++ {
								if t[k].s == "from" {
									tb = "-" // from(…) já contou
								}
								if t[k].k == 'i' && isUpper(t[k].s) {
									mm, _ := exMod(t, k)
									tb = schemas[expand(mm)]
								}
							}
						}
						for k := i - 1; k >= 0 && k > i-40 && tb == "" && t[k].s != "=" && !(t[k].k == 'i' && (t[k].s == "def" || t[k].s == "defp" || t[k].s == "do")); k-- {
							if t[k].k == 'i' && isUpper(t[k].s) && (k == 0 || t[k-1].s != ".") {
								mm, _ := exMod(t, k)
								tb = schemas[expand(mm)]
							}
						}
						if tb != "" && tb != "-" {
							g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: tk.line, From: c.from(), Type: "table", Name: tb, Ops: []string{op}, Client: "Ecto"})
						}
					}
				}
				i = j + 2
				continue
			}
			if d := decls[full]; d != nil && !(d.file == f.rel && d.line == tk.line) {
				ref(tk.line, d, kind)
			}
			i = j
			continue
		}
		// chamada local: funcao(…), |> funcao, &funcao/1
		if i+1 < len(t) && t[i+1].s == ":" {
			continue
		}
		var d *exDecl
		if m := c.mod(); m != "" {
			d = decls[m+"."+tk.s]
		}
		for _, im := range imports {
			if d == nil {
				d = decls[im+"."+tk.s]
			}
		}
		if d != nil && !(d.file == f.rel && d.line == tk.line) {
			kind := "call"
			if i > 0 && t[i-1].s == "&" {
				kind = "ref"
			}
			ref(tk.line, d, kind)
		}
	}
	if isRouter {
		f.phoenixRoutes(decls, g)
	}
}

type exRouteCtx struct {
	path, alias string
	pipes       []string // pipe_through e live_session em vigor
	pipeline    string   // dentro de pipeline :x do … end
}

var phxActions = []struct{ name, method, suffix string }{
	{"index", "GET", ""}, {"edit", "GET", "/:id/edit"}, {"new", "GET", "/new"}, {"show", "GET", "/:id"},
	{"create", "POST", ""}, {"update", "PATCH", "/:id"}, {"update", "PUT", "/:id"}, {"delete", "DELETE", "/:id"},
}

func (f *exFile) phoenixRoutes(decls map[string]*exDecl, g *model.Graph) {
	t := f.toks
	stack := []exRouteCtx{{}}
	pipelines := map[string][]string{}
	guardsOf := func(cur exRouteCtx) string {
		var out []string
		for _, p := range cur.pipes {
			out = append(out, p)
			out = append(out, pipelines[p]...)
		}
		if len(out) == 0 {
			return "none"
		}
		return strings.Join(out, ",")
	}
	modOf := func(cur exRouteCtx, m string) string {
		if cur.alias != "" {
			return cur.alias + "." + m
		}
		return m
	}
	var guards string
	emit := func(line int, method, path, mod, fn string) {
		h, hf := mod+"."+fn, ""
		if d := decls[h]; d != nil {
			hf = d.file
		} else if d := decls[mod]; d != nil {
			hf = d.file
		}
		if path == "" {
			path = "/"
		}
		g.Routes = append(g.Routes, model.Route{File: f.rel, Method: method, Path: path, Line: line, HandlerFile: hf, Handler: h, Guards: guards})
	}
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if tk.k != 'i' || i > 0 && t[i-1].s == "." {
			continue
		}
		cur := stack[len(stack)-1]
		guards = guardsOf(cur)
		switch tk.s {
		case "end":
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		case "do":
			if i+1 < len(t) && t[i+1].s == ":" {
				continue
			}
			stack = append(stack, cur)
			continue
		case "fn":
			stack = append(stack, cur)
			continue
		}
		// argumentos até o fim da linha (ou o do do bloco)
		end := i + 1
		for end < len(t) && t[end].line == tk.line && !(t[end].s == "do" && !(end+1 < len(t) && t[end+1].s == ":")) {
			end++
		}
		var strs, mods, atoms []string
		opts := map[string][]string{}
		for k := i + 1; k < end; k++ {
			switch {
			case t[k].k == 'i' && k+1 < end && t[k+1].s == ":":
				key := t[k].s
				k += 2
				d := 0
				for ; k < end; k++ {
					if t[k].s == "[" {
						d++
					} else if t[k].s == "]" {
						d--
					} else if d == 0 && t[k].s == "," {
						break
					}
					if t[k].k == 's' || t[k].k == 'y' || t[k].k == 'i' {
						v := t[k].s
						if t[k].k == 'i' && isUpper(v) {
							v, k = exMod(t, k)
						}
						opts[key] = append(opts[key], v)
					}
				}
			case t[k].k == 's':
				strs = append(strs, t[k].s)
			case t[k].k == 'y':
				atoms = append(atoms, t[k].s)
			case t[k].k == 'i' && isUpper(t[k].s):
				m, e := exMod(t, k)
				mods = append(mods, m)
				k = e
			}
		}
		block := end < len(t) && t[end].s == "do"
		child := cur
		child.pipes = append([]string{}, cur.pipes...)
		child.pipeline = ""
		switch tk.s {
		case "pipeline":
			if len(atoms) > 0 {
				child.pipeline = atoms[0]
			}
		case "plug":
			if cur.pipeline != "" {
				pipelines[cur.pipeline] = append(append(pipelines[cur.pipeline], atoms...), mods...)
			}
		case "pipe_through":
			top := &stack[len(stack)-1]
			top.pipes = append(top.pipes, atoms...)
		case "live_session":
			child.pipes = append(child.pipes, atoms...)
			for _, v := range opts["on_mount"] {
				child.pipes = append(child.pipes, v)
			}
		case "scope":
			if len(strs) > 0 {
				child.path = rbJoin(cur.path, strs[0])
			}
			if v := opts["path"]; len(v) > 0 {
				child.path = rbJoin(cur.path, v[0])
			}
			if len(mods) > 0 {
				child.alias = modOf(cur, mods[0])
			}
			if v := opts["alias"]; len(v) > 0 {
				child.alias = modOf(cur, v[0])
			}
		case "get", "post", "put", "patch", "delete", "options", "head":
			if len(strs) > 0 && len(mods) > 0 && len(atoms) > 0 {
				emit(tk.line, strings.ToUpper(tk.s), rbJoin(cur.path, strs[0]), modOf(cur, mods[0]), atoms[0])
			}
		case "live":
			if len(strs) > 0 && len(mods) > 0 {
				m := modOf(cur, mods[0])
				path := rbJoin(cur.path, strs[0])
				if d := decls[m]; d != nil {
					g.Pages = append(g.Pages, model.Page{File: d.file, Path: path, Line: d.line, Component: m})
				}
				fn := "mount"
				emit(tk.line, "GET", path, m, fn)
			}
		case "resources":
			if len(strs) > 0 && len(mods) > 0 {
				only, except := map[string]bool{}, map[string]bool{}
				for _, v := range opts["only"] {
					only[v] = true
				}
				for _, v := range opts["except"] {
					except[v] = true
				}
				base := rbJoin(cur.path, strs[0])
				m := modOf(cur, mods[0])
				for _, a := range phxActions {
					if len(only) > 0 && !only[a.name] || except[a.name] {
						continue
					}
					emit(tk.line, a.method, base+a.suffix, m, a.name)
				}
				seg := strings.Trim(strs[0], "/")
				seg = seg[strings.LastIndexByte(seg, '/')+1:]
				child.path = base + "/:" + singular(seg) + "_id"
			}
		}
		if block {
			stack = append(stack, child)
		}
		i = end
		if !block {
			i = end - 1
		}
	}
}
