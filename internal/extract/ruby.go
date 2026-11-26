package extract

// Ruby / Rails sem interpretador: classes e módulos (A::B), métodos como
// Classe.metodo (def e def self.), escopo por class/module/def/do/end. Os usos
// seguem as constantes pelo aninhamento léxico (como o Ruby resolve), chamadas
// Classe.metodo, métodos da própria classe e símbolos que nomeiam métodos
// (before_action :autenticar). Do Rails: config/routes.rb vira rotas
// (resources, namespace, scope, member/collection, get "x", to: "c#a"), os
// modelos ActiveRecord viram tabelas (Pedido → pedidos, ou self.table_name) e
// Pedido.where/create/update_all viram uso de tabela com a operação.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

var rbSkip = map[string]bool{"node_modules": true, "vendor": true, "tmp": true, "log": true, "coverage": true, "public": true, "storage": true, "migrate": true}

type rbFile struct {
	rel   string
	toks  []tok
	lines int
}

type rbDecl struct {
	file, name, kind string
	line             int
}

type rbIndex struct {
	decls    map[string]*rbDecl
	supers   map[string]string // classe → superclasse já resolvida
	tables   map[string]string // classe → tabela (só modelos ActiveRecord)
	tname    map[string]string // classe → self.table_name
	abs      map[string]bool   // self.abstract_class = true
	filters  map[string][]rbFilter
	includes map[string][]string // classe → módulos incluídos (concerns)
}

// rbFilter é um before_action (ou skip_before_action) de um controller.
type rbFilter struct {
	name         string
	only, except map[string]bool
	skip         bool
}

// guards devolve os before_action que valem para Classe#acao, somando os
// das superclasses e tirando os skip_before_action. "none" se não houver.
func (idx *rbIndex) guards(cls, action string) string {
	var chain []string
	for c, n := cls, 0; c != "" && n < 10; c, n = idx.supers[c], n+1 {
		chain = append([]string{c}, chain...)
	}
	var active []string
	for _, c := range chain {
		var fl []rbFilter
		for _, m := range idx.includes[c] {
			fl = append(fl, idx.filters[m]...) // included do before_action … end
		}
		for _, f := range append(fl, idx.filters[c]...) {
			if len(f.only) > 0 && !f.only[action] || f.except[action] {
				continue
			}
			if f.skip {
				kept := active[:0]
				for _, a := range active {
					if a != f.name && !(f.name == "*auth" && rbAuthFilter(a)) {
						kept = append(kept, a)
					}
				}
				active = kept
				continue
			}
			active = append(active, f.name)
		}
	}
	if len(active) == 0 {
		return "none"
	}
	return strings.Join(active, ",")
}

func rbAuthFilter(n string) bool {
	n = strings.ToLower(n)
	return strings.Contains(n, "auth") || strings.Contains(n, "login") || strings.Contains(n, "signed_in") || strings.HasPrefix(n, "require_")
}

type rbScope struct {
	kind byte // 'c' classe/módulo, 's' class << self, 'f' def, 'b' bloco
	name string
}

type rbCtx struct {
	stack    []rbScope
	loopLine int
}

func (c *rbCtx) class() string {
	for k := len(c.stack) - 1; k >= 0; k-- {
		if c.stack[k].kind == 'c' || c.stack[k].kind == 's' {
			return c.stack[k].name
		}
	}
	return ""
}

func (c *rbCtx) from() string {
	for k := len(c.stack) - 1; k >= 0; k-- {
		switch c.stack[k].kind {
		case 'f', 'c', 's':
			return c.stack[k].name
		}
	}
	return "<module>"
}

// nesting: as classes/módulos abertos, de fora para dentro (para resolver constantes).
func (c *rbCtx) nesting() []string {
	var out []string
	for _, s := range c.stack {
		if s.kind == 'c' {
			out = append(out, s.name)
		}
	}
	return out
}

var rbStmtPrev = map[string]bool{";": true, "=": true, "(": true, "[": true, ",": true, "|": true, "&": true, "{": true, "then": true, "do": true, "else": true, "return": true, "?": true, ":": true, "!": true, "not": true, "and": true, "or": true}

func rbStmtStart(t []tok, i int) bool {
	return i == 0 || t[i-1].line < t[i].line || rbStmtPrev[t[i-1].s]
}

// rbConst lê A::B::C a partir de i e devolve o caminho e o índice do último token.
func rbConst(t []tok, i int) (string, int) {
	var parts []string
	j := i
	if t[j].s == "::" {
		j++
	}
	for j < len(t) && t[j].k == 'i' {
		parts = append(parts, t[j].s)
		if j+2 < len(t) && t[j+1].s == "::" && t[j+2].k == 'i' && isUpper(t[j+2].s) {
			j += 2
			continue
		}
		break
	}
	return strings.Join(parts, "::"), j
}

func isUpper(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }

// step trata os tokens que abrem e fecham escopos. Devolve o escopo aberto
// (para quem precisa saber o nome do que foi declarado).
func (c *rbCtx) step(t []tok, i int) (opened *rbScope, decl string, kind string, skip int) {
	tk := t[i]
	if tk.k != 'i' || i > 0 && (t[i-1].s == "." || t[i-1].s == "::") || i+1 < len(t) && t[i+1].s == ":" && t[i+1].line == tk.line && !(i+2 < len(t) && t[i+2].s == ":") {
		return nil, "", "", 0
	}
	push := func(s rbScope) *rbScope {
		c.stack = append(c.stack, s)
		return &c.stack[len(c.stack)-1]
	}
	switch tk.s {
	case "class", "module":
		if i+2 < len(t) && t[i+1].s == "<" && t[i+2].s == "<" {
			return push(rbScope{kind: 's', name: c.class()}), "", "", 2
		}
		if i+1 >= len(t) || !(t[i+1].k == 'i' || t[i+1].s == "::") {
			return nil, "", "", 0
		}
		name, j := rbConst(t, i+1)
		full := name
		if t[i+1].s != "::" {
			if cls := c.class(); cls != "" {
				full = cls + "::" + name
			}
		}
		return push(rbScope{kind: 'c', name: full}), full, tk.s, j - i
	case "def":
		j := i + 1
		single := false
		if j+2 < len(t) && (t[j].s == "self" || t[j].k == 'i' && isUpper(t[j].s)) && t[j+1].s == "." {
			single = true
			j += 2
		}
		name := ""
		if j < len(t) && t[j].k == 'i' {
			name = t[j].s
			if j+1 < len(t) && t[j+1].s == "=" && t[j+1].line == t[j].line && j+2 < len(t) && t[j+2].s == "(" {
				name += "=" // def nome=(v)
				j++
			}
		}
		// def x = expr (sem end)
		k := j + 1
		if k < len(t) && t[k].s == "(" {
			if e := matchParen(t, k); e > 0 {
				k = e + 1
			}
		}
		endless := k < len(t) && t[k].s == "=" && t[k].line == t[j].line && !strings.HasSuffix(name, "=")
		cls := c.class()
		full := name
		if cls != "" {
			full = cls + "." + name
		}
		_ = single
		if name == "" {
			return push(rbScope{kind: 'f', name: c.from()}), "", "", 0
		}
		if endless {
			return nil, full, "method", j - i
		}
		return push(rbScope{kind: 'f', name: full}), full, "method", j - i
	case "do":
		if c.loopLine == tk.line {
			c.loopLine = 0
			return nil, "", "", 0
		}
		return push(rbScope{kind: 'b'}), "", "", 0
	case "begin", "case":
		return push(rbScope{kind: 'b'}), "", "", 0
	case "if", "unless", "while", "until", "for":
		if !rbStmtStart(t, i) {
			return nil, "", "", 0
		}
		if tk.s == "while" || tk.s == "until" || tk.s == "for" {
			c.loopLine = tk.line
		}
		return push(rbScope{kind: 'b'}), "", "", 0
	case "end":
		if len(c.stack) > 0 {
			c.stack = c.stack[:len(c.stack)-1]
		}
	}
	return nil, "", "", 0
}

func rubyFiles(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || rbSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".rb") || strings.HasSuffix(p, ".rake") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Ruby extrai o código Ruby (e o Rails). Com only, só esses arquivos entram no grafo.
func Ruby(root string, g *model.Graph, only []string) error {
	var files []*rbFile
	for _, p := range rubyFiles(root) {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		files = append(files, &rbFile{rel: filepath.ToSlash(rel), toks: lexHash(src, hashRuby), lines: strings.Count(string(src), "\n") + 1})
	}
	idx := &rbIndex{decls: map[string]*rbDecl{}, supers: map[string]string{}, tables: map[string]string{}, tname: map[string]string{}, abs: map[string]bool{}, filters: map[string][]rbFilter{}, includes: map[string][]string{}}
	type rawSuper struct {
		cls, path string
		nest      []string
	}
	var raws, rawIncludes []rawSuper
	all := make([][]*rbDecl, len(files))
	for fi, f := range files {
		t := f.toks
		c := &rbCtx{}
		for i := 0; i < len(t); i++ {
			nest := c.nesting()
			_, name, kind, skip := c.step(t, i)
			if name != "" && kind != "" {
				if kind == "method" && strings.HasSuffix(name, ".") {
					continue
				}
				d := &rbDecl{file: f.rel, name: name, kind: kind, line: t[i].line}
				all[fi] = append(all[fi], d)
				if _, dup := idx.decls[name]; !dup {
					idx.decls[name] = d
				}
				if kind == "class" {
					j := i + skip + 1
					if j+1 < len(t) && t[j].s == "<" && t[j].line == t[i].line && (t[j+1].k == 'i' || t[j+1].s == "::") {
						p, _ := rbConst(t, j+1)
						raws = append(raws, rawSuper{name, p, nest})
					}
				}
			}
			// before_action :autenticar, only: [:x] / skip_before_action / include Authentication
			if t[i].k == 'i' && c.class() != "" && !strings.Contains(c.from(), ".") && rbStmtStart(t, i) {
				end, ln := i+1, t[i].line
				for end < len(t) && (t[end].line == ln || t[end-1].s == ",") {
					ln = t[end].line
					end++
				}
				cls := c.class()
				add := func(name string, skip bool, a rbArgs) {
					f := rbFilter{name: name, skip: skip, only: map[string]bool{}, except: map[string]bool{}}
					for _, v := range a.list("only") {
						f.only[v] = true
					}
					for _, v := range a.list("except") {
						f.except[v] = true
					}
					idx.filters[cls] = append(idx.filters[cls], f)
				}
				switch t[i].s {
				case "before_action", "prepend_before_action", "before_filter", "skip_before_action", "skip_before_filter":
					a := rbParseArgs(t, i+1, end)
					for _, p := range a.pos {
						if p.k == 'y' {
							add(p.s, strings.HasPrefix(t[i].s, "skip_"), a)
						}
					}
				case "allow_unauthenticated_access", "skip_authentication":
					add("*auth", true, rbParseArgs(t, i+1, end))
				case "include":
					for k := i + 1; k < end; k++ {
						if t[k].k == 'i' && isUpper(t[k].s) && (k == i+1 || t[k-1].s == ",") {
							p, e := rbConst(t, k)
							rawIncludes = append(rawIncludes, rawSuper{cls, p, c.nesting()})
							k = e
						}
					}
				}
			}
			// self.table_name = "x" / self.abstract_class = true
			if t[i].s == "self" && i+3 < len(t) && t[i+1].s == "." && t[i+3].s == "=" {
				switch t[i+2].s {
				case "table_name":
					if i+4 < len(t) && (t[i+4].k == 's' || t[i+4].k == 'y') {
						idx.tname[c.class()] = t[i+4].s
					}
				case "abstract_class":
					if i+4 < len(t) && t[i+4].s == "true" {
						idx.abs[c.class()] = true
					}
				}
			}
			i += skip
		}
	}
	for _, r := range raws {
		idx.supers[r.cls] = idx.resolve(r.path, r.nest)
	}
	for _, r := range rawIncludes {
		idx.includes[r.cls] = append(idx.includes[r.cls], idx.resolve(r.path, r.nest))
	}
	// modelos: herdam de ActiveRecord::Base, direta ou indiretamente
	var isModel func(cls string, depth int) bool
	isModel = func(cls string, depth int) bool {
		s := idx.supers[cls]
		if s == "ActiveRecord::Base" || s == "ApplicationRecord" && idx.decls["ApplicationRecord"] == nil {
			return true
		}
		if s == "" || depth > 8 {
			return false
		}
		return isModel(s, depth+1)
	}
	for cls := range idx.supers {
		if idx.abs[cls] || !isModel(cls, 0) {
			continue
		}
		tb := idx.tname[cls]
		if tb == "" {
			tb = plural(snake(cls[strings.LastIndex(cls, "::")+1:]))
		}
		idx.tables[cls] = tb
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
		f.uses(idx, g)
		if f.rel == "config/routes.rb" || strings.HasPrefix(f.rel, "config/routes/") {
			f.routes(idx, g, root)
		}
	}
	return nil
}

// resolve acha a constante como o Ruby: do aninhamento mais fundo para fora.
func (idx *rbIndex) resolve(path string, nest []string) string {
	if strings.HasPrefix(path, "::") {
		return strings.TrimPrefix(path, "::")
	}
	for k := len(nest); k > 0; k-- {
		if c := nest[k-1] + "::" + path; idx.decls[c] != nil {
			return c
		}
	}
	return path
}

var arOps = map[string]string{
	"create": "insert", "create!": "insert", "insert": "insert", "insert_all": "insert", "insert_all!": "insert", "find_or_create_by": "insert", "find_or_create_by!": "insert", "create_or_find_by": "insert", "create_or_find_by!": "insert",
	"update": "update", "update!": "update", "update_all": "update", "update_columns": "update", "update_column": "update", "upsert": "upsert", "upsert_all": "upsert", "update_counters": "update", "increment_counter": "update", "decrement_counter": "update", "touch_all": "update",
	"destroy": "delete", "destroy!": "delete", "destroy_all": "delete", "delete": "delete", "delete_all": "delete", "destroy_by": "delete", "delete_by": "delete",
	"all": "select", "where": "select", "find": "select", "find_by": "select", "find_by!": "select", "find_each": "select", "find_in_batches": "select", "first": "select", "last": "select", "take": "select",
	"pluck": "select", "count": "select", "exists?": "select", "sum": "select", "average": "select", "minimum": "select", "maximum": "select", "order": "select", "limit": "select", "includes": "select",
	"joins": "select", "left_joins": "select", "eager_load": "select", "preload": "select", "select": "select", "distinct": "select", "group": "select", "offset": "select", "ids": "select",
	"find_or_initialize_by": "select", "find_by_sql": "select", "none": "select", "not": "select", "or": "select", "in_batches": "select", "page": "select", "with": "select", "having": "select",
}

// chainOp anda pela corrente .a(…).b(…) a partir de j e devolve a operação de escrita, se houver.
func chainOp(t []tok, j int, op string) string {
	for j+1 < len(t) && t[j].s == "." && t[j+1].k == 'i' {
		if o := arOps[t[j+1].s]; o != "" && o != "select" {
			op = o
		}
		j += 2
		if j < len(t) && t[j].s == "(" {
			e := matchParen(t, j)
			if e < 0 {
				break
			}
			j = e + 1
		}
	}
	return op
}

func (f *rbFile) uses(idx *rbIndex, g *model.Graph) {
	t := f.toks
	c := &rbCtx{}
	// x = Classe.new / @x ||= Classe.new: o tipo da variável
	vtypes := map[string]string{}
	for i := 3; i < len(t); i++ {
		if t[i].s != "new" || t[i-1].s != "." || t[i-2].k != 'i' || !isUpper(t[i-2].s) {
			continue
		}
		j := i - 2
		for j >= 2 && t[j-1].s == "::" {
			j -= 2
		}
		path, _ := rbConst(t, j)
		k := j - 1
		if k < 1 || t[k].s != "=" {
			continue
		}
		k--
		for k > 0 && t[k].s == "|" {
			k--
		}
		if t[k].k == 'v' || t[k].k == 'i' && !isUpper(t[k].s) {
			vtypes[t[k].s] = path
		}
	}
	ref := func(line int, d *rbDecl, kind string) {
		g.Refs = append(g.Refs, model.Ref{File: f.rel, Line: line, From: c.from(), ToFile: d.file, To: d.name, Kind: kind})
	}
	for i := 0; i < len(t); i++ {
		tk := t[i]
		_, name, _, skip := c.step(t, i)
		if name != "" || skip > 0 {
			// o nome declarado não é uso; a superclasse é
			j := i + skip + 1
			if tk.s == "class" && j+1 < len(t) && t[j].s == "<" && t[j].line == tk.line {
				p, _ := rbConst(t, j+1)
				if d := idx.decls[idx.resolve(p, c.nesting()[:max(0, len(c.nesting())-1)])]; d != nil {
					ref(tk.line, d, "type")
				}
				_, e := rbConst(t, j+1)
				i = e
				continue
			}
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
		case 'v':
			// @servico.cobrar
			if vt := vtypes[tk.s]; vt != "" && i+2 < len(t) && t[i+1].s == "." && t[i+2].k == 'i' {
				if d := idx.decls[idx.resolve(vt, c.nesting())+"."+t[i+2].s]; d != nil {
					ref(t[i+2].line, d, "call")
				}
			}
			continue
		case 'y':
			// before_action :autenticar, validate :checar, after_create :avisar
			if cls := c.class(); cls != "" {
				if d := idx.decls[cls+"."+tk.s]; d != nil && !(d.file == f.rel && d.line == tk.line) {
					ref(tk.line, d, "ref")
				}
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
		if isUpper(tk.s) && prev != "." && prev != "::" {
			path, last := rbConst(t, i)
			if i > 0 && t[i-1].s == "::" {
				path = "::" + path
			}
			cls := idx.resolve(path, c.nesting())
			d := idx.decls[cls]
			j := last + 1
			if d != nil {
				kind := "ref"
				if j+1 < len(t) && t[j].s == "." && t[j+1].s == "new" {
					kind = "new"
				}
				ref(tk.line, d, kind)
			}
			if j+1 < len(t) && t[j].s == "." && t[j+1].k == 'i' {
				m := t[j+1].s
				if md := idx.decls[cls+"."+m]; md != nil {
					ref(t[j+1].line, md, "call")
				} else if tb := idx.tables[cls]; tb != "" {
					if op := arOps[m]; op != "" {
						k := j + 2
						if k < len(t) && t[k].s == "(" {
							if e := matchParen(t, k); e > 0 {
								k = e + 1
							}
						}
						g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: t[j+1].line, From: c.from(), Type: "table", Name: tb, Ops: []string{chainOp(t, k, op)}, Client: "ActiveRecord"})
					}
				}
				i = j + 1
				continue
			}
			i = last
			continue
		}
		// variavel.metodo pelo tipo de x = Classe.new
		if vt := vtypes[tk.s]; vt != "" && prev != "." && i+2 < len(t) && t[i+1].s == "." && t[i+2].k == 'i' {
			if d := idx.decls[idx.resolve(vt, c.nesting())+"."+t[i+2].s]; d != nil {
				ref(t[i+2].line, d, "call")
				i += 2
				continue
			}
		}
		// método da própria classe: salvar / self.salvar / salvar(x)
		if prev == "." && !(i > 1 && t[i-2].s == "self") || prev == "def" || prev == "::" {
			continue
		}
		if i+1 < len(t) && t[i+1].s == "=" && t[i+1].line == tk.line && !(i+2 < len(t) && t[i+2].s == "=") {
			continue // atribuição a variável local
		}
		cls := c.class()
		if d := idx.decls[cls+"."+tk.s]; d != nil && cls != "" {
			if !(d.file == f.rel && d.line == tk.line) {
				ref(tk.line, d, "call")
			}
			continue
		}
		if d := idx.decls[tk.s]; d != nil && d.kind == "method" && !(d.file == f.rel && d.line == tk.line) {
			ref(tk.line, d, "call")
		}
	}
}

// ---- config/routes.rb ----

type rbRouteCtx struct {
	path  string // prefixo de caminho
	mod   string // prefixo de controller ("admin/")
	ctl   string // controller padrão (controller :x do / dentro de resources)
	mode  byte   // 'm' member, 'c' collection
	res   string // caminho do recurso (/pedidos) para member/collection
	resID string // /pedidos/:id
}

type rbArgs struct {
	pos  []tok
	opts map[string][]tok
	to   string // get "x" => "c#a"
}

func rbParseArgs(t []tok, i, end int) rbArgs {
	a := rbArgs{opts: map[string][]tok{}}
	j := i
	for j < end {
		tk := t[j]
		switch {
		case tk.k == 'i' && j+1 < end && t[j+1].s == ":" && !(j+2 < end && t[j+2].s == ":"):
			key := tk.s
			j += 2
			var vals []tok
			d := 0
			for j < end {
				s := t[j].s
				if t[j].k == 'p' && (s == "[" || s == "(" || s == "{") {
					d++
				} else if t[j].k == 'p' && (s == "]" || s == ")" || s == "}") {
					d--
				} else if d == 0 && s == "," && t[j].k == 'p' {
					break
				}
				if t[j].k != 'p' {
					vals = append(vals, t[j])
				}
				j++
			}
			a.opts[key] = vals
		case tk.s == "=>" && j+1 < end && t[j+1].k == 's':
			a.to = t[j+1].s
			j += 2
		case tk.k == 's' || tk.k == 'y':
			if j+1 < end && t[j+1].s == "=>" && j+2 < end && t[j+2].k == 's' {
				a.pos = append(a.pos, tk)
				a.to = t[j+2].s
				j += 3
				continue
			}
			a.pos = append(a.pos, tk)
			j++
		default:
			j++
		}
	}
	return a
}

func (a rbArgs) opt(k string) string {
	if v := a.opts[k]; len(v) > 0 {
		return v[0].s
	}
	return ""
}

func (a rbArgs) list(k string) []string {
	var out []string
	for _, v := range a.opts[k] {
		out = append(out, v.s)
	}
	return out
}

var resActions = []struct {
	name, method, suffix string
	member               bool
}{
	{"index", "GET", "", false}, {"create", "POST", "", false}, {"new", "GET", "/new", false},
	{"show", "GET", "", true}, {"edit", "GET", "/edit", true}, {"update", "PATCH", "", true}, {"update", "PUT", "", true}, {"destroy", "DELETE", "", true},
}

func rbJoin(a, b string) string {
	if b == "" {
		if a == "" {
			return "/"
		}
		return a
	}
	if strings.HasPrefix(b, "/") {
		return strings.TrimRight(a, "/") + b
	}
	return strings.TrimRight(a, "/") + "/" + b
}

func (f *rbFile) routes(idx *rbIndex, g *model.Graph, root string) {
	t := f.toks
	stack := []rbRouteCtx{{}}
	emit := func(line int, method, path, ctl, action string) {
		cls := ""
		for _, seg := range strings.Split(strings.Trim(ctl, "/"), "/") {
			if cls != "" {
				cls += "::"
			}
			cls += camel(seg)
		}
		cls += "Controller"
		h := cls + "." + action
		hf := ""
		if d := idx.decls[h]; d != nil {
			hf = d.file
		} else if d := idx.decls[cls]; d != nil {
			hf = d.file
		} else if p := "app/controllers/" + strings.Trim(ctl, "/") + "_controller.rb"; exists(filepath.Join(root, p)) {
			hf = p
		}
		if path == "" {
			path = "/"
		}
		gs := ""
		if idx.decls[cls] != nil {
			gs = idx.guards(cls, action)
		}
		g.Routes = append(g.Routes, model.Route{File: f.rel, Method: method, Path: path, Line: line, HandlerFile: hf, Handler: h, Guards: gs})
	}
	ctlOf := func(cur rbRouteCtx, c string) string {
		if strings.HasPrefix(c, "/") {
			return c
		}
		return cur.mod + c
	}
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if tk.k != 'i' || !rbStmtStart(t, i) || i > 0 && t[i-1].s == "." {
			continue
		}
		if tk.s == "end" {
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		// fim do comando: fim da linha sem vírgula pendente, ou o do do bloco
		end, d := i+1, 0
		for end < len(t) {
			s := t[end]
			if s.k == 'p' && (s.s == "(" || s.s == "[" || s.s == "{") {
				d++
			} else if s.k == 'p' && (s.s == ")" || s.s == "]" || s.s == "}") {
				d--
			}
			if d <= 0 && s.k == 'i' && s.s == "do" {
				break
			}
			if d <= 0 && end+1 < len(t) && t[end+1].line > s.line && s.s != "," && s.s != "=>" && s.s != "\\" {
				end++
				break
			}
			end++
		}
		block := end < len(t) && t[end].s == "do"
		a := rbParseArgs(t, i+1, end)
		cur := stack[len(stack)-1]
		child := cur
		child.mode = 0
		switch tk.s {
		case "namespace":
			if len(a.pos) > 0 {
				n := a.pos[0].s
				p := n
				if v := a.opt("path"); v != "" {
					p = v
				}
				child.path = rbJoin(cur.path, p)
				child.mod = cur.mod + n + "/"
			}
		case "scope":
			if len(a.pos) > 0 {
				child.path = rbJoin(cur.path, a.pos[0].s)
			}
			if v := a.opt("path"); v != "" {
				child.path = rbJoin(cur.path, v)
			}
			if v := a.opt("module"); v != "" {
				child.mod = cur.mod + v + "/"
			}
			if v := a.opt("controller"); v != "" {
				child.ctl = v
			}
		case "controller":
			if len(a.pos) > 0 {
				child.ctl = a.pos[0].s
			}
		case "member", "collection":
			child.mode = tk.s[0]
		case "if", "unless", "case", "begin", "while", "until":
			block = true
			end = i
			for end+1 < len(t) && t[end+1].line == tk.line {
				end++
			}
		case "resources", "resource":
			only, except := map[string]bool{}, map[string]bool{}
			for _, v := range a.list("only") {
				only[v] = true
			}
			for _, v := range a.list("except") {
				except[v] = true
			}
			param := "id"
			if v := a.opt("param"); v != "" {
				param = v
			}
			for _, p := range a.pos {
				name := p.s
				ctl := name
				if tk.s == "resource" {
					ctl = plural(name)
				}
				if v := a.opt("controller"); v != "" {
					ctl = v
				}
				seg := name
				if v := a.opt("path"); v != "" {
					seg = v
				}
				base := rbJoin(cur.path, seg)
				for _, act := range resActions {
					if len(only) > 0 && !only[act.name] || except[act.name] {
						continue
					}
					if tk.s == "resource" && act.name == "index" {
						continue
					}
					path := base
					if act.member && tk.s == "resources" {
						path += "/:" + param
					}
					emit(p.line, act.method, path+act.suffix, ctlOf(cur, ctl), act.name)
				}
				child.ctl = ctl
				child.res = base
				child.resID = base
				if tk.s == "resources" {
					child.resID = base + "/:" + param
					child.path = base + "/:" + singular(name) + "_" + param
				} else {
					child.path = base
				}
				child.mod = cur.mod
				if a.opt("controller") != "" {
					child.ctl = a.opt("controller")
				}
				// o controller do bloco é o do recurso, mas sem repetir o módulo
				child.ctl = strings.TrimPrefix(child.ctl, cur.mod)
			}
		case "root":
			to := a.to
			if v := a.opt("to"); v != "" {
				to = v
			}
			if len(a.pos) > 0 && strings.Contains(a.pos[0].s, "#") {
				to = a.pos[0].s
			}
			if k := strings.IndexByte(to, '#'); k > 0 {
				emit(tk.line, "GET", rbJoin(cur.path, ""), ctlOf(cur, to[:k]), to[k+1:])
			}
		case "get", "post", "put", "patch", "delete", "match":
			methods := []string{strings.ToUpper(tk.s)}
			if tk.s == "match" {
				methods = nil
				for _, v := range a.list("via") {
					if v == "all" {
						v = "get"
					}
					methods = append(methods, strings.ToUpper(v))
				}
				if len(methods) == 0 {
					methods = []string{"GET"}
				}
			}
			p := ""
			if len(a.pos) > 0 {
				p = a.pos[0].s
			}
			to := a.to
			if v := a.opt("to"); v != "" {
				to = v
			}
			ctl, action := "", ""
			if k := strings.IndexByte(to, '#'); k > 0 {
				ctl, action = to[:k], to[k+1:]
			}
			if v := a.opt("controller"); v != "" {
				ctl = v
			}
			if v := a.opt("action"); v != "" {
				action = v
			}
			on := a.opt("on")
			mode := cur.mode
			if on == "member" {
				mode = 'm'
			} else if on == "collection" {
				mode = 'c'
			}
			base := cur.path
			switch mode {
			case 'm':
				base = cur.resID
			case 'c':
				base = cur.res
			}
			name := strings.Trim(p, "/")
			if action == "" {
				action = name
				if k := strings.LastIndexByte(name, '/'); k >= 0 {
					action = name[k+1:]
					if ctl == "" && cur.ctl == "" {
						ctl = name[:k]
					}
				}
			}
			if ctl == "" {
				ctl = cur.ctl
			}
			if ctl == "" || action == "" {
				continue
			}
			for _, m := range methods {
				emit(tk.line, m, rbJoin(base, p), ctlOf(cur, ctl), strings.TrimSuffix(action, "?"))
			}
		}
		if block {
			stack = append(stack, child)
			i = end
		} else {
			i = end - 1
		}
	}
}
