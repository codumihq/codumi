package extract

// Swift sem compilador. Os arquivos de um app enxergam tudo do próprio
// módulo sem import, então as declarações valem para o projeto inteiro:
// tipos (class, struct, enum, protocol, actor; telas SwiftUI viram
// component), funções, métodos como Tipo.metodo (inclusive em extension) e
// globais. Os usos seguem tipos, Tipo.metodo, self.metodo e métodos do
// próprio tipo. Também acha chamadas /api/..., o Supabase (.from("t"),
// .rpc("f")) e as rotas do Vapor (app.get("x", ":id", use: h), grouped).

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

var swiftSkip = map[string]bool{"node_modules": true, "Pods": true, "Carthage": true, "DerivedData": true, "build": true, "checkouts": true}

type swFile struct {
	rel   string
	toks  []tok
	lines int
}

type swDecl struct {
	file, name, kind string
	line             int
}

type swScope struct {
	kind byte // 't' tipo, 'f' função, 'b' bloco
	name string
}

type swCtx struct {
	stack   []swScope
	pending swScope
}

func (c *swCtx) typ() string {
	for k := len(c.stack) - 1; k >= 0; k-- {
		if c.stack[k].kind == 't' {
			return c.stack[k].name
		}
	}
	return ""
}

func (c *swCtx) from() string {
	if c.pending.kind == 'f' {
		return c.pending.name // assinatura: parâmetros e retorno são da função
	}
	for k := len(c.stack) - 1; k >= 0; k-- {
		if c.stack[k].kind == 't' || c.stack[k].kind == 'f' {
			return c.stack[k].name
		}
	}
	return "<module>"
}

func (c *swCtx) declLevel() bool { return len(c.stack) == 0 || c.stack[len(c.stack)-1].kind == 't' }

var swTypeKw = map[string]string{"class": "class", "struct": "struct", "enum": "enum", "protocol": "interface", "actor": "class"}

// step abre e fecha escopos e devolve a declaração feita no token i.
func (c *swCtx) step(t []tok, i int) (name, kind string) {
	tk := t[i]
	if tk.k == 'p' {
		switch tk.s {
		case "{":
			s := c.pending
			if s.kind == 0 {
				s.kind = 'b'
			}
			c.stack = append(c.stack, s)
			c.pending = swScope{}
		case "}":
			if len(c.stack) > 0 {
				c.stack = c.stack[:len(c.stack)-1]
			}
			c.pending = swScope{}
		}
		return "", ""
	}
	if tk.k != 'i' || !c.declLevel() || i+1 >= len(t) || i > 0 && t[i-1].s == "." {
		return "", ""
	}
	next := t[i+1]
	outer := c.typ()
	qual := func(n string) string {
		if outer != "" {
			return outer + "." + n
		}
		return n
	}
	if k, ok := swTypeKw[tk.s]; ok && next.k == 'i' && next.s != "func" && next.s != "var" && next.s != "let" {
		full := qual(next.s)
		// struct Tela: View { … } é uma tela do SwiftUI
		for j := i + 2; j < len(t) && j < i+30 && t[j].s != "{"; j++ {
			if t[j].s == "View" && (t[j-1].s == ":" || t[j-1].s == ",") {
				k = "component"
			}
		}
		c.pending = swScope{kind: 't', name: full}
		return full, k
	}
	switch tk.s {
	case "extension":
		var parts []string
		for j := i + 1; j < len(t) && t[j].k == 'i'; j += 2 {
			parts = append(parts, t[j].s)
			if j+1 >= len(t) || t[j+1].s != "." {
				break
			}
		}
		c.pending = swScope{kind: 't', name: strings.Join(parts, ".")}
	case "func":
		if next.k != 'i' {
			c.pending = swScope{kind: 'f', name: c.from()}
			return "", ""
		}
		full := qual(next.s)
		c.pending = swScope{kind: 'f', name: full}
		if outer != "" {
			return full, "method"
		}
		return full, "function"
	case "init", "deinit", "subscript":
		if outer != "" && (next.s == "(" || next.s == "?" || next.s == "!" || next.s == "{" || next.s == "<") {
			c.pending = swScope{kind: 'f', name: outer + "." + tk.s}
		}
	case "var", "let":
		if outer == "" && next.k == 'i' && len(c.stack) == 0 {
			return next.s, "var"
		}
	case "typealias":
		if next.k == 'i' {
			return qual(next.s), "type"
		}
	}
	return "", ""
}

// Swift extrai o código Swift. Com only, só esses arquivos entram no grafo.
func Swift(root string, g *model.Graph, only []string) error {
	var files []*swFile
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || swiftSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".swift") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		files = append(files, &swFile{rel: filepath.ToSlash(rel), toks: lex(src, false), lines: strings.Count(string(src), "\n") + 1})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	decls := map[string]*swDecl{}
	all := make([][]*swDecl, len(files))
	for fi, f := range files {
		c := &swCtx{}
		for i := range f.toks {
			if name, kind := c.step(f.toks, i); name != "" {
				d := &swDecl{file: f.rel, name: name, kind: kind, line: f.toks[i].line}
				all[fi] = append(all[fi], d)
				if _, dup := decls[name]; !dup {
					decls[name] = d
				}
			}
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
		f.uses(decls, g)
	}
	return nil
}

func (f *swFile) uses(decls map[string]*swDecl, g *model.Graph) {
	t := f.toks
	c := &swCtx{}
	prefix := map[string]string{} // let api = app.grouped("api") → api: /api
	vtypes := jvmVarTypes(t)      // let s = Servico() / var s: Servico / (s: Servico)
	ref := func(line int, d *swDecl, kind string) {
		g.Refs = append(g.Refs, model.Ref{File: f.rel, Line: line, From: c.from(), ToFile: d.file, To: d.name, Kind: kind})
	}
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if name, _ := c.step(t, i); name != "" {
			i++ // o nome declarado
			continue
		}
		switch tk.k {
		case 's':
			prev := ""
			if i > 0 {
				prev = t[i-1].s
			}
			if prev == "(" || prev == ":" && i > 1 && t[i-2].s == "string" {
				if p, ok := apiRoute(tk.s); ok && !(i > 2 && httpVerb[t[i-2].s] != "" && t[i-3].s == ".") {
					g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: f.rel, Line: tk.line, From: c.from(), Path: p})
				}
			}
			if looksSQL.MatchString(tk.s) {
				sqlUse(g, f.rel, tk.line, c.from(), tk.s, "")
			}
			// supabase.from("t").select() / .rpc("f") / storage.from("b")
			if i > 2 && prev == "(" && t[i-3].s == "." && (t[i-2].s == "from" || t[i-2].s == "rpc") && i+1 < len(t) && t[i+1].s == ")" {
				typ, recv := "table", ""
				if i > 3 {
					recv = t[i-4].s
				}
				if t[i-2].s == "rpc" {
					typ = "rpc"
				} else if recv == "storage" {
					typ = "bucket"
				}
				var ops []string
				for k := i + 2; k < len(t) && k < i+30; k++ {
					if t[k].k == 'i' && t[k-1].s == "." && (tableOps[t[k].s] || typ == "bucket" && (t[k].s == "upload" || t[k].s == "download" || t[k].s == "remove")) {
						ops = append(ops, t[k].s)
						break
					}
				}
				g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: tk.line, From: c.from(), Type: typ, Name: tk.s, Ops: ops, Client: recv})
			}
			continue
		case 'i':
		default:
			continue
		}
		prev := ""
		if i > 0 {
			prev = t[i-1].s
		}
		// Vapor: app.get("pedidos", ":id", use: handler) / let api = app.grouped("api")
		if prev == "." && i+2 < len(t) && t[i+1].s == "(" && (httpVerb[tk.s] != "" || tk.s == "grouped") && i > 1 {
			end := matchParen(t, i+1)
			if end > 0 {
				var segs []string
				handler := ""
				for k := i + 2; k < end; k++ {
					if t[k].k == 's' && (t[k-1].s == "(" || t[k-1].s == ",") {
						segs = append(segs, strings.Trim(t[k].s, "/"))
					}
					if t[k].s == "use" && k+2 < end && t[k+1].s == ":" {
						for h := k + 2; h < end; h++ {
							if t[h].k == 'i' && t[h].s != "self" {
								handler = t[h].s
							}
						}
					}
				}
				recv := t[i-2].s
				path := strings.TrimRight(prefix[recv], "/") + "/" + strings.Join(segs, "/")
				if tk.s == "grouped" {
					if i > 4 && t[i-3].s == "=" && (t[i-5].s == "let" || t[i-5].s == "var") {
						prefix[t[i-4].s] = path
					}
				} else if len(segs) > 0 || handler != "" || end+1 < len(t) && t[end+1].s == "{" {
					h, hf := c.from(), f.rel
					if handler != "" {
						h = handler
						if d := decls[c.typ()+"."+handler]; d != nil {
							h, hf = d.name, d.file
						} else if d := decls[handler]; d != nil {
							h, hf = d.name, d.file
						}
					}
					g.Routes = append(g.Routes, model.Route{File: f.rel, Method: httpVerb[tk.s], Path: path, Line: tk.line, HandlerFile: hf, Handler: h})
				}
			}
		}
		switch prev {
		case "func", "class", "struct", "enum", "protocol", "actor", "extension", "typealias", "case":
			continue
		case ".":
			// self.metodo / Tipo.metodo / Tipo.Aninhado
			if i < 2 {
				continue
			}
			owner := t[i-2].s
			if decls[owner] == nil && vtypes[owner] != "" && !(i > 2 && t[i-3].s == ".") {
				owner = vtypes[owner]
			}
			if owner == "self" || owner == "Self" {
				owner = c.typ()
			} else if t[i-2].k != 'i' || decls[owner] == nil {
				continue
			} else {
				owner = decls[owner].name
			}
			if d := decls[owner+"."+tk.s]; d != nil {
				kind := "ref"
				if i+1 < len(t) && t[i+1].s == "(" {
					kind = "call"
				}
				ref(tk.line, d, kind)
			}
			continue
		}
		if i+1 < len(t) && t[i+1].s == ":" && (prev == "(" || prev == ",") {
			continue // rótulo de argumento
		}
		var d *swDecl
		if typ := c.typ(); typ != "" && !isUpper(tk.s) {
			d = decls[typ+"."+tk.s]
		}
		if d == nil {
			d = decls[tk.s]
		}
		if d == nil || d.file == f.rel && d.line == tk.line {
			continue
		}
		kind := "ref"
		switch {
		case i+1 < len(t) && (t[i+1].s == "(" || t[i+1].s == "{" && d.kind == "component"):
			if isUpper(tk.s) {
				kind = "new"
			} else {
				kind = "call"
			}
		case isUpper(tk.s) && (prev == ":" || prev == "->" || prev == "<" || prev == "[" || prev == "as" || prev == "is"):
			kind = "type"
		}
		ref(tk.line, d, kind)
	}
}
