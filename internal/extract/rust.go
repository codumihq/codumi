package extract

// Rust sem compilador: módulos pelos arquivos (lib.rs/main.rs, a.rs, a/mod.rs)
// e pelos mod { } dentro do código, declarações (fn, struct, enum, trait,
// type, const, static, macro_rules!) e métodos dos impl como Tipo.metodo.
// Os usos são seguidos pelos caminhos (crate::, self::, super::, outro crate
// do workspace), pelos use (com alias, listas e *), por Self:: e self.metodo.
// Também acha rotas (axum .route, atributos #[get] do actix/rocket), chamadas
// /api/... e SQL em texto.

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

type rsCrate struct {
	dir, name string
}

type rsFile struct {
	rel   string
	toks  []tok
	lines int
	crate string // nome do crate
	mod   string // caminho do módulo dentro do crate ("" = raiz), ex.: "a::b"
}

type rsDecl struct {
	file, name, kind string
	key              string // crate::módulo em que foi declarada
	line             int
	exported         bool
}

type rsIndex struct {
	decls   map[string]map[string]*rsDecl // crate::mod → nome → declaração
	modules map[string]bool               // crate::mod existentes
	crates  map[string]bool
}

func modKey(crate, mod string) string {
	if mod == "" {
		return crate
	}
	return crate + "::" + mod
}

func rustCrates(root string) []rsCrate {
	var out []rsCrate
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "target" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "Cargo.toml" {
			out = append(out, rsCrate{dir: filepath.Dir(p), name: cargoName(p)})
		}
		return nil
	})
	// o crate mais fundo primeiro, para cada arquivo achar o seu
	sort.Slice(out, func(i, j int) bool { return len(out[i].dir) > len(out[j].dir) })
	return out
}

func cargoName(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return filepath.Base(filepath.Dir(p))
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	section := ""
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "[") {
			section = l
			continue
		}
		if (section == "[package]" || section == "[lib]") && strings.HasPrefix(l, "name") {
			if i := strings.IndexByte(l, '='); i > 0 {
				return strings.ReplaceAll(strings.Trim(strings.TrimSpace(l[i+1:]), `"'`), "-", "_")
			}
		}
	}
	return strings.ReplaceAll(filepath.Base(filepath.Dir(p)), "-", "_")
}

// rsModule acha o crate e o módulo de um arquivo .rs.
func rsModule(crates []rsCrate, abs string) (string, string) {
	for _, c := range crates {
		if !strings.HasPrefix(abs, c.dir+string(filepath.Separator)) {
			continue
		}
		rel, _ := filepath.Rel(filepath.Join(c.dir, "src"), abs)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "bin/") {
			// build.rs, tests/, examples/, src/bin/: cada um é a raiz de si mesmo
			r, _ := filepath.Rel(c.dir, abs)
			return c.name + "#" + strings.TrimSuffix(filepath.ToSlash(r), ".rs"), ""
		}
		if rel == "lib.rs" || rel == "main.rs" {
			return c.name, ""
		}
		rel = strings.TrimSuffix(rel, ".rs")
		rel = strings.TrimSuffix(rel, "/mod")
		return c.name, strings.ReplaceAll(rel, "/", "::")
	}
	return "", ""
}

// Rust extrai o código Rust do projeto. Com only, só esses arquivos entram no grafo.
func Rust(root string, g *model.Graph, only []string) error {
	crates := rustCrates(root)
	var files []*rsFile
	for _, c := range crates {
		filepath.WalkDir(c.dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p != c.dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "target") {
					return filepath.SkipDir
				}
				if p != c.dir && exists(filepath.Join(p, "Cargo.toml")) {
					return filepath.SkipDir // outro crate: lido por ele mesmo
				}
				return nil
			}
			if !strings.HasSuffix(p, ".rs") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			cr, mod := rsModule(crates, p)
			files = append(files, &rsFile{rel: filepath.ToSlash(rel), toks: lex(src, true), lines: strings.Count(string(src), "\n") + 1, crate: cr, mod: mod})
			return nil
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	idx := &rsIndex{decls: map[string]map[string]*rsDecl{}, modules: map[string]bool{}, crates: map[string]bool{}}
	var all [][]*rsDecl
	for _, f := range files {
		idx.crates[f.crate] = true
		ds := f.decls(idx)
		all = append(all, ds)
	}
	want := map[string]bool{}
	for _, o := range only {
		want[o] = true
	}
	for i, f := range files {
		if only != nil && !want[f.rel] {
			continue
		}
		g.Files = append(g.Files, model.File{Path: f.rel, Lines: f.lines})
		for _, d := range all[i] {
			g.Decls = append(g.Decls, model.Decl{File: d.file, Name: d.name, Kind: d.kind, Line: d.line, Exported: d.exported})
		}
		f.uses(idx, g)
	}
	return nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

type rsScope struct {
	kind byte   // 'm' módulo, 'i' impl, 't' trait, 'f' função, 'b' bloco
	name string // módulo (caminho completo), tipo do impl/trait, ou origem da função
}

type rsCtx struct {
	stack   []rsScope
	mod     string // módulo atual
	typ     string // tipo do impl/trait mais próximo
	from    string
	pending rsScope
}

func (c *rsCtx) refresh(base string) {
	c.mod, c.typ, c.from = base, "", "<module>"
	for _, s := range c.stack {
		switch s.kind {
		case 'm':
			c.mod, c.typ, c.from = s.name, "", "<module>"
		case 'i', 't':
			c.typ, c.from = s.name, s.name
		case 'f':
			c.from = s.name
		}
	}
}

// onlyModules diz se só há módulos, impl e trait abertos (nível de declaração).
func (c *rsCtx) declLevel() bool {
	for _, s := range c.stack {
		if s.kind == 'f' || s.kind == 'b' {
			return false
		}
	}
	return true
}

func (c *rsCtx) open(base string) {
	s := c.pending
	if s.kind == 0 {
		s.kind = 'b'
	}
	c.stack = append(c.stack, s)
	c.pending = rsScope{}
	c.refresh(base)
}

func (c *rsCtx) close(base string) {
	if len(c.stack) > 0 {
		c.stack = c.stack[:len(c.stack)-1]
	}
	c.refresh(base)
}

func rsPub(t []tok, i int) bool {
	for j := i - 1; j >= 0 && j >= i-8; j-- {
		switch t[j].s {
		case "pub":
			return t[j].k == 'i'
		case ";", "{", "}", "]":
			if t[j].k == 'p' {
				return false
			}
		}
	}
	return false
}

// implType lê o tipo de um impl: impl<T> Trait for Tipo<T> { … }.
func implType(t []tok, i int) (string, int) {
	j := i + 1
	if j < len(t) && t[j].s == "<" {
		j = skipAngle(t, j)
	}
	name := ""
	for ; j < len(t); j++ {
		s := t[j]
		if s.k == 'p' && (s.s == "{" || s.s == ";") {
			return name, j
		}
		if s.k == 'i' && s.s == "for" {
			name = ""
			continue
		}
		if s.k == 'i' && s.s == "where" {
			for j < len(t) && t[j].s != "{" {
				j++
			}
			return name, j
		}
		if s.s == "<" {
			j = skipAngle(t, j) - 1
			continue
		}
		if s.k == 'i' && s.s != "dyn" && s.s != "mut" && s.s != "const" {
			name = s.s
		}
	}
	return name, j
}

func (f *rsFile) decls(idx *rsIndex) []*rsDecl {
	var out []*rsDecl
	t := f.toks
	c := &rsCtx{}
	c.refresh(f.mod)
	reg := func(mod, name, kind string, line int, pub bool) {
		k := modKey(f.crate, mod)
		d := &rsDecl{file: f.rel, name: name, kind: kind, line: line, exported: pub, key: k}
		out = append(out, d)
		if idx.decls[k] == nil {
			idx.decls[k] = map[string]*rsDecl{}
		}
		if _, dup := idx.decls[k][name]; !dup {
			idx.decls[k][name] = d
		}
	}
	idx.modules[modKey(f.crate, f.mod)] = true
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if tk.k == 'p' {
			switch tk.s {
			case "{":
				c.open(f.mod)
			case "}":
				c.close(f.mod)
			case ";":
				c.pending = rsScope{}
			}
			continue
		}
		if tk.k != 'i' || !c.declLevel() || i+1 >= len(t) {
			continue
		}
		inType := len(c.stack) > 0 && (c.stack[len(c.stack)-1].kind == 'i' || c.stack[len(c.stack)-1].kind == 't')
		next := t[i+1]
		switch tk.s {
		case "mod":
			if next.k != 'i' {
				continue
			}
			sub := next.s
			if c.mod != "" {
				sub = c.mod + "::" + next.s
			}
			idx.modules[modKey(f.crate, sub)] = true
			reg(c.mod, next.s, "module", tk.line, rsPub(t, i))
			if i+2 < len(t) && t[i+2].s == "{" {
				c.pending = rsScope{kind: 'm', name: sub}
			}
			i++
		case "fn":
			if next.k != 'i' {
				continue
			}
			if inType {
				name := c.typ + "." + next.s
				reg(c.mod, name, "method", tk.line, rsPub(t, i) || c.stack[len(c.stack)-1].kind == 't')
				c.pending = rsScope{kind: 'f', name: name}
			} else {
				reg(c.mod, next.s, "function", tk.line, rsPub(t, i))
				c.pending = rsScope{kind: 'f', name: next.s}
			}
			i++
		case "struct", "enum", "union", "type", "const", "static":
			if inType || next.k != 'i' || (tk.s == "union" && !(i+2 < len(t) && (t[i+2].s == "{" || t[i+2].s == "<"))) {
				continue
			}
			if next.s == "mut" && i+2 < len(t) {
				next = t[i+2]
				i++
			}
			reg(c.mod, next.s, tk.s, tk.line, rsPub(t, i))
			i++
		case "trait":
			if next.k != 'i' {
				continue
			}
			reg(c.mod, next.s, "trait", tk.line, rsPub(t, i))
			c.pending = rsScope{kind: 't', name: next.s}
			i++
		case "impl":
			name, j := implType(t, i)
			if name != "" && j < len(t) && t[j].s == "{" {
				c.pending = rsScope{kind: 'i', name: name}
				i = j - 1
			}
		case "macro_rules":
			if next.s == "!" && i+2 < len(t) && t[i+2].k == 'i' {
				reg(c.mod, t[i+2].s, "macro", tk.line, true)
				i += 2
			}
		}
	}
	return out
}

// rsUse lê uma árvore de use e devolve alias → caminho e os caminhos com *.
func rsUse(t []tok, i int) (map[string][]string, [][]string, int) {
	alias := map[string][]string{}
	var globs [][]string
	var walk func(j int, prefix []string) int
	walk = func(j int, prefix []string) int {
		path := append([]string{}, prefix...)
		for j < len(t) {
			s := t[j]
			switch {
			case s.k == 'i' && s.s == "as":
				if j+1 < len(t) && path != nil {
					if t[j+1].s != "_" {
						alias[t[j+1].s] = path
					}
				}
				return j + 2
			case s.k == 'i':
				path = append(path, s.s)
				j++
			case s.s == "::":
				j++
			case s.s == "*":
				globs = append(globs, path)
				return j + 1
			case s.s == "{":
				j++
				for j < len(t) && t[j].s != "}" {
					j = walk(j, path)
					if j < len(t) && t[j].s == "," {
						j++
					}
				}
				return j + 1
			default:
				if len(path) > len(prefix) {
					last := path[len(path)-1]
					if last == "self" {
						path = path[:len(path)-1]
						if len(path) > 0 {
							alias[path[len(path)-1]] = path
						}
					} else {
						alias[last] = path
					}
				}
				return j
			}
		}
		return j
	}
	j := walk(i+1, nil)
	for j < len(t) && t[j].s != ";" {
		j++
	}
	return alias, globs, j
}

// resolve transforma um caminho escrito no código em (módulo, nome).
func (f *rsFile) resolve(idx *rsIndex, mod string, alias map[string][]string, globs [][]string, typ string, segs []string) *rsDecl {
	if len(segs) == 0 {
		return nil
	}
	crate := f.crate
	var base []string // módulo de partida (dentro de crate)
	cur := []string{}
	if mod != "" {
		cur = strings.Split(mod, "::")
	}
	first := segs[0]
	rest := segs[1:]
	switch {
	case first == "crate":
		base = []string{}
	case first == "self":
		base = cur
	case first == "super":
		base = cur
		for len(rest) >= 0 {
			if len(base) > 0 {
				base = base[:len(base)-1]
			}
			if len(rest) > 0 && rest[0] == "super" {
				rest = rest[1:]
				continue
			}
			break
		}
	case first == "Self" && typ != "":
		if len(rest) == 1 {
			if d := idx.decls[modKey(crate, mod)][typ+"."+rest[0]]; d != nil {
				return d
			}
		}
		return nil
	case alias[first] != nil:
		full := append(append([]string{}, alias[first]...), rest...)
		if full[0] == first && len(alias[first]) == 1 && !idx.crates[first] {
			return nil
		}
		return f.resolve(idx, mod, nil, nil, typ, full)
	case idx.crates[first] && first != crate:
		crate, base = first, []string{}
	case len(segs) == 1:
		if d := idx.decls[modKey(crate, mod)][first]; d != nil {
			return d
		}
		for _, gp := range globs {
			if d := f.resolve(idx, mod, nil, nil, typ, append(append([]string{}, gp...), first)); d != nil {
				return d
			}
		}
		return nil
	default:
		// módulo filho do atual (a::b::c a partir daqui) ou do crate
		base = cur
		rest = segs
		if !idx.modules[modKey(crate, strings.Join(append(append([]string{}, cur...), first), "::"))] {
			if !idx.modules[modKey(crate, first)] {
				return nil
			}
			base = []string{}
		}
	}
	// anda pelos módulos enquanto existirem
	m := base
	for len(rest) > 1 {
		nm := append(append([]string{}, m...), rest[0])
		if idx.modules[modKey(crate, strings.Join(nm, "::"))] {
			m = nm
			rest = rest[1:]
			continue
		}
		break
	}
	k := modKey(crate, strings.Join(m, "::"))
	switch len(rest) {
	case 1:
		return idx.decls[k][rest[0]]
	case 2: // Tipo::metodo
		return idx.decls[k][rest[0]+"."+rest[1]]
	}
	return nil
}

func (f *rsFile) uses(idx *rsIndex, g *model.Graph) {
	t := f.toks
	alias := map[string][]string{}
	var globs [][]string
	ref := func(line int, from string, d *rsDecl, kind string) {
		g.Refs = append(g.Refs, model.Ref{File: f.rel, Line: line, From: from, ToFile: d.file, To: d.name, Kind: kind})
	}
	c := &rsCtx{}
	c.refresh(f.mod)
	var attrRoute *model.Route
	inAttr := 0 // profundidade dentro de #[ … ]
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if tk.k == 'p' {
			switch tk.s {
			case "{":
				c.open(f.mod)
			case "}":
				c.close(f.mod)
			case ";":
				c.pending = rsScope{}
			case "#":
				if i+1 < len(t) && (t[i+1].s == "[" || t[i+1].s == "!" && i+2 < len(t) && t[i+2].s == "[") {
					// #[get("/x")] do actix/rocket
					j := i + 1
					if t[j].s == "!" {
						j++
					}
					d := 0
					for ; j < len(t); j++ {
						if t[j].s == "[" {
							d++
						} else if t[j].s == "]" {
							d--
							if d == 0 {
								break
							}
						} else if d == 1 && t[j].k == 'i' && httpVerb[t[j].s] != "" && (t[j-1].s == "[" || t[j-1].s == "::") &&
							j+2 < len(t) && t[j+1].s == "(" && t[j+2].k == 's' {
							attrRoute = &model.Route{File: f.rel, Method: httpVerb[t[j].s], Path: t[j+2].s, Line: t[j].line}
						}
					}
					i = j
				}
			}
			_ = inAttr
			continue
		}
		if tk.k == 's' {
			if looksSQL.MatchString(tk.s) {
				sqlUse(g, f.rel, tk.line, c.from, tk.s, "")
			}
			if i > 0 && t[i-1].s == "(" && !(i > 2 && t[i-2].s == "route" && t[i-3].s == ".") {
				if p, ok := apiRoute(tk.s); ok {
					g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: f.rel, Line: tk.line, From: c.from, Path: p})
				}
			}
			continue
		}
		if tk.k != 'i' {
			continue
		}
		switch tk.s {
		case "use":
			if c.declLevel() || true {
				a, gl, j := rsUse(t, i)
				for k, v := range a {
					alias[k] = v
					if d := f.resolve(idx, c.mod, nil, nil, c.typ, v); d != nil {
						ref(tk.line, "<module>", d, "import")
					}
				}
				globs = append(globs, gl...)
				i = j
				continue
			}
		case "mod":
			if i+2 < len(t) && t[i+1].k == 'i' && t[i+2].s == "{" {
				sub := t[i+1].s
				if c.mod != "" {
					sub = c.mod + "::" + sub
				}
				c.pending = rsScope{kind: 'm', name: sub}
			}
			i++
			continue
		case "fn":
			if i+1 < len(t) && t[i+1].k == 'i' {
				name := t[i+1].s
				if len(c.stack) > 0 && (c.stack[len(c.stack)-1].kind == 'i' || c.stack[len(c.stack)-1].kind == 't') {
					name = c.typ + "." + name
				}
				if c.declLevel() {
					c.pending = rsScope{kind: 'f', name: name}
					c.from = name
				}
				if attrRoute != nil {
					attrRoute.HandlerFile, attrRoute.Handler = f.rel, name
					g.Routes = append(g.Routes, *attrRoute)
					attrRoute = nil
				}
				i++
				continue
			}
		case "trait":
			if c.declLevel() && i+1 < len(t) {
				c.pending = rsScope{kind: 't', name: t[i+1].s}
				i++
				continue
			}
		case "impl":
			if c.declLevel() {
				if name, j := implType(t, i); name != "" && j < len(t) && t[j].s == "{" {
					c.pending = rsScope{kind: 'i', name: name}
				}
			}
			continue
		case "struct", "enum", "union", "type", "const", "static", "macro_rules":
			i++
			continue
		case "route":
			// axum: .route("/x", get(handler).post(outro))
			if i > 0 && t[i-1].s == "." && i+3 < len(t) && t[i+1].s == "(" && t[i+2].k == 's' {
				end := matchParen(t, i+1)
				if end < 0 {
					end = len(t) - 1
				}
				for j := i + 3; j < end; j++ {
					if m := httpVerb[t[j].s]; m != "" && t[j].k == 'i' && j+1 < end && t[j+1].s == "(" {
						var segs []string
						k := j + 2
						for k < end && (t[k].k == 'i' || t[k].s == "::") {
							if t[k].k == 'i' {
								segs = append(segs, t[k].s)
							}
							k++
						}
						h := strings.Join(segs, "::")
						if d := f.resolve(idx, c.mod, alias, globs, c.typ, segs); d != nil {
							h = d.name
						}
						g.Routes = append(g.Routes, model.Route{File: f.rel, Method: m, Path: t[i+2].s, Line: t[i+2].line, HandlerFile: f.rel, Handler: h})
					}
				}
			}
		}
		// self.metodo(): método do próprio impl
		if i > 1 && t[i-1].s == "." {
			if t[i-2].s == "self" && c.typ != "" && i+1 < len(t) && t[i+1].s == "(" {
				if d := idx.decls[modKey(f.crate, c.mod)][c.typ+"."+tk.s]; d != nil {
					ref(tk.line, c.from, d, "call")
				}
			}
			continue
		}
		if i > 0 && t[i-1].s == "::" {
			continue // parte de um caminho já tratado
		}
		// caminho a::b::C (pula ::<T>)
		segs := []string{tk.s}
		j := i + 1
		for j+1 < len(t) && t[j].s == "::" {
			if t[j+1].s == "<" {
				j = skipAngle(t, j+1)
				continue
			}
			if t[j+1].k != 'i' {
				break
			}
			segs = append(segs, t[j+1].s)
			j += 2
		}
		last := j - 1
		d := f.resolve(idx, c.mod, alias, globs, c.typ, segs)
		if d == nil && len(segs) > 1 {
			// Tipo::metodo de um tipo importado ou do módulo
			if td := f.resolve(idx, c.mod, alias, globs, c.typ, segs[:len(segs)-1]); td != nil {
				if md := idx.decls[td.key][td.name+"."+segs[len(segs)-1]]; md != nil {
					d = md
				}
			}
		}
		if d != nil && !(d.file == f.rel && d.line == tk.line && len(segs) == 1) {
			kind := "ref"
			if j < len(t) && (t[j].s == "(" || t[j].s == "!") {
				kind = "call"
			} else if i > 0 && (t[i-1].s == "impl" || t[i-1].s == "for" || t[i-1].s == "->" || t[i-1].s == ":" || t[i-1].s == "&" || t[i-1].s == "<") {
				kind = "type"
			} else if j < len(t) && t[j].s == "{" && d.kind == "struct" {
				kind = "new"
			}
			ref(t[last].line, c.from, d, kind)
		}
		i = last
	}
}
