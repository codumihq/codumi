package extract

// Kotlin e Java sem compilador: declarações (classes, funções, membros) e
// usos seguidos pelo pacote do arquivo, pelos imports (inclusive com alias e
// com *) e por Classe.membro / this.membro. Chamadas em objetos de tipo
// desconhecido não são seguidas. Também acha rotas (Spring, Ktor), chamadas
// de API (Retrofit e textos /api/...), Supabase (from/rpc/storage) e SQL.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

var jvmSkip = map[string]bool{"build": true, "out": true, "target": true, "node_modules": true, ".gradle": true, ".idea": true, "generated": true, "bin": true, "obj": true}

var jvmKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "new": true, "throw": true, "synchronized": true,
	"when": true, "else": true, "try": true, "do": true, "super": true, "this": true, "is": true, "in": true, "as": true, "assert": true,
}

var springMap = map[string]string{"GetMapping": "GET", "PostMapping": "POST", "PutMapping": "PUT", "DeleteMapping": "DELETE", "PatchMapping": "PATCH", "RequestMapping": "",
	// ASP.NET
	"HttpGet": "GET", "HttpPost": "POST", "HttpPut": "PUT", "HttpDelete": "DELETE", "HttpPatch": "PATCH"}
var retrofitMap = map[string]string{"GET": "GET", "POST": "POST", "PUT": "PUT", "DELETE": "DELETE", "PATCH": "PATCH", "HEAD": "HEAD"}
var httpVerb = map[string]string{"get": "GET", "post": "POST", "put": "PUT", "delete": "DELETE", "patch": "PATCH", "head": "HEAD", "options": "OPTIONS"}

type jvmImport struct {
	path, alias string
	star        bool
}

type jvmFile struct {
	rel     string
	toks    []tok
	lines   int
	pkg     string
	imports []jvmImport
	java    bool // Java ou C#: métodos são tipo nome(…)
	cs      bool // C#
	// autenticação global do ASP.NET: FallbackPolicy ou MapControllers().RequireAuthorization()
	globalAuth, groupAuth bool
}

type jvmDecl struct {
	file, name, kind string
	line             int
	exported         bool
}

// jvmIndex guarda o que cada pacote declara, para resolver nomes.
type jvmIndex struct {
	byPkg   map[string]map[string]*jvmDecl // pacote → nome → declaração (topo e Classe.membro)
	members map[string]map[string]bool     // pacote.Classe → membros
}

func jvmFiles(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || jvmSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".kt", ".kts", ".java", ".cs", ".scala":
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Kotlin extrai Kotlin e Java. Com only, só esses arquivos entram no grafo
// (o projeto inteiro é lido para resolver os nomes).
func Kotlin(root string, g *model.Graph, only []string) error {
	var files []*jvmFile
	for _, p := range jvmFiles(root) {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		cs := strings.HasSuffix(p, ".cs")
		toks := lex(src, false)
		if cs {
			toks = csAttributes(toks)
		}
		if strings.HasSuffix(p, ".scala") {
			toks = scalaToks(toks)
		}
		f := &jvmFile{rel: filepath.ToSlash(rel), toks: toks, lines: strings.Count(string(src), "\n") + 1, java: cs || strings.HasSuffix(p, ".java"), cs: cs}
		f.header()
		files = append(files, f)
	}
	sets := efSets(files)
	globalAuth, groupAuth := false, false
	for _, f := range files {
		if !f.cs {
			continue
		}
		for i, tk := range f.toks {
			if tk.s == "FallbackPolicy" || tk.s == "RequireAuthorization" && i > 4 && f.toks[i-4].s == "MapControllers" {
				globalAuth = true
			} else if tk.s == "RequireAuthorization" {
				groupAuth = true
			}
		}
	}
	for _, f := range files {
		f.globalAuth, f.groupAuth = globalAuth, groupAuth
	}
	idx := &jvmIndex{byPkg: map[string]map[string]*jvmDecl{}, members: map[string]map[string]bool{}}
	var decls [][]*jvmDecl
	for _, f := range files {
		ds := f.decls()
		decls = append(decls, ds)
		if idx.byPkg[f.pkg] == nil {
			idx.byPkg[f.pkg] = map[string]*jvmDecl{}
		}
		for _, d := range ds {
			if _, dup := idx.byPkg[f.pkg][d.name]; !dup {
				idx.byPkg[f.pkg][d.name] = d
			}
			if i := strings.IndexByte(d.name, '.'); i > 0 {
				k := f.pkg + "." + d.name[:i]
				if idx.members[k] == nil {
					idx.members[k] = map[string]bool{}
				}
				idx.members[k][d.name[i+1:]] = true
			}
		}
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
		for _, d := range decls[i] {
			g.Decls = append(g.Decls, model.Decl{File: d.file, Name: d.name, Kind: d.kind, Line: d.line, Exported: d.exported})
		}
		f.uses(idx, g, sets)
	}
	if only == nil {
		playRoutes(root, g)
	}
	return nil
}

func (f *jvmFile) header() {
	t := f.toks
	for i := 0; i < len(t); i++ {
		if t[i].k != 'i' {
			continue
		}
		switch t[i].s {
		case "package":
			f.pkg, _ = dotted(t, i+1)
		case "import":
			j := i + 1
			if j < len(t) && t[j].s == "static" {
				j++
			}
			path, end := dotted(t, j)
			im := jvmImport{path: path}
			if strings.HasSuffix(path, ".*") {
				im.star, im.path = true, strings.TrimSuffix(path, ".*")
			} else if end+1 < len(t) && t[end].s == "as" {
				im.alias = t[end+1].s
			}
			if im.alias == "" && !im.star {
				im.alias = path[strings.LastIndexByte(path, '.')+1:]
			}
			f.imports = append(f.imports, im)
		case "namespace":
			if f.cs {
				f.pkg, _ = dotted(t, i+1)
			}
		case "using":
			// using A.B; (todo o namespace) e using X = A.B.C;
			if !f.cs || i+1 >= len(t) || t[i+1].s == "(" || t[i+1].s == "var" {
				continue
			}
			j := i + 1
			if t[j].s == "static" || t[j].s == "global" {
				j++
			}
			if j+1 < len(t) && t[j+1].s == "=" {
				path, _ := dotted(t, j+2)
				f.imports = append(f.imports, jvmImport{path: path, alias: t[j].s})
			} else {
				path, _ := dotted(t, j)
				f.imports = append(f.imports, jvmImport{path: path, star: true})
			}
		case "class", "interface", "object", "fun", "enum", "record", "struct":
			if f.cs {
				// os namespaces de cima também são vistos sem using
				for p := f.pkg; strings.Contains(p, "."); {
					p = p[:strings.LastIndexByte(p, '.')]
					f.imports = append(f.imports, jvmImport{path: p, star: true})
				}
			}
			return // o cabeçalho acabou
		}
	}
}

// dotted lê a.b.C (ou a.b.*) a partir de i e devolve o texto e o fim.
func dotted(t []tok, i int) (string, int) {
	var b strings.Builder
	for i < len(t) {
		if t[i].k == 'i' {
			b.WriteString(t[i].s)
		} else if t[i].s == "*" {
			b.WriteString("*")
			return b.String(), i + 1
		} else {
			break
		}
		i++
		if i < len(t) && t[i].s == "." && i+1 < len(t) && (t[i+1].k == 'i' || t[i+1].s == "*") {
			b.WriteByte('.')
			i++
			continue
		}
		break
	}
	return b.String(), i
}

// scope é um par de chaves aberto: corpo de classe, de função ou bloco comum.
type jvmScope struct {
	kind   byte   // 'c' classe, 'f' função, 'b' bloco, 'r' rota (Ktor route), 'n' namespace do C#
	name   string // classe ou origem (função)
	prefix string // prefixo de rota (Spring na classe, Ktor route)
	auth   byte   // 'y' [Authorize] na classe, 'n' [AllowAnonymous]
}

// walk percorre o arquivo chamando visit em cada token com o contexto atual.
type jvmCtx struct {
	stack   []jvmScope
	class   string // classe mais próxima
	from    string // declaração em que o token está
	prefix  string // prefixo de rota vigente
	depth   int    // profundidade de parênteses
	pending jvmScope
	expr    string // método com corpo de expressão (=> …;) em andamento
	auth    byte   // autenticação da classe vigente
}

// topLevel: nenhum escopo aberto além de namespaces.
func (c *jvmCtx) topLevel() bool {
	for _, s := range c.stack {
		if s.kind != 'n' {
			return false
		}
	}
	return true
}

func (c *jvmCtx) refresh() {
	c.class, c.from, c.prefix, c.auth = "", "<module>", "", 0
	for _, s := range c.stack {
		switch s.kind {
		case 'c':
			c.class, c.from, c.auth = s.name, s.name, s.auth
		case 'f':
			c.from = s.name
		}
		if s.prefix != "" {
			c.prefix = joinPath(c.prefix, s.prefix)
		}
	}
	if c.expr != "" {
		c.from = c.expr
	}
}

func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	return strings.TrimRight(a, "/") + "/" + strings.TrimLeft(b, "/")
}

// decls acha as declarações do arquivo: tipos e funções do topo e membros das classes.
func (f *jvmFile) decls() []*jvmDecl {
	var out []*jvmDecl
	t := f.toks
	c := &jvmCtx{}
	c.refresh()
	add := func(name, kind string, line int, exported bool) {
		out = append(out, &jvmDecl{file: f.rel, name: name, kind: kind, line: line, exported: exported})
	}
	for i := 0; i < len(t); i++ {
		tk := t[i]
		switch {
		case tk.s == "(" && tk.k == 'p':
			c.depth++
		case tk.s == ")" && tk.k == 'p':
			c.depth--
		case tk.s == "{" && tk.k == 'p':
			s := c.pending
			if s.kind == 0 {
				s.kind = 'b'
			}
			c.stack = append(c.stack, s)
			c.pending = jvmScope{}
			c.refresh()
		case tk.s == "}" && tk.k == 'p':
			if len(c.stack) > 0 {
				c.stack = c.stack[:len(c.stack)-1]
			}
			c.refresh()
		case tk.s == ";" && tk.k == 'p':
			c.pending = jvmScope{}
		case tk.k == 'i' && c.depth == 0:
			inClassBody := len(c.stack) > 0 && c.stack[len(c.stack)-1].kind == 'c'
			top := c.topLevel()
			if !top && !inClassBody {
				continue
			}
			if tk.s == "namespace" && f.cs {
				c.pending = jvmScope{kind: 'n'}
				_, j := dotted(t, i+1)
				i = j - 1
				continue
			}
			exported := !hasMod(t, i, "private") && !hasMod(t, i, "internal") && (!f.java || hasMod(t, i, "public") || inClassBody && hasMod(t, i, "protected"))
			switch tk.s {
			case "class", "interface", "object", "enum", "record", "struct":
				if tk.s == "enum" && i+1 < len(t) && t[i+1].s == "class" {
					continue
				}
				if i > 0 && t[i-1].s == "." || i > 0 && t[i-1].s == "::" { // X.class, X::class
					continue
				}
				if i+1 >= len(t) || t[i+1].k != 'i' {
					if tk.s == "object" && i > 0 && t[i-1].s == "companion" && inClassBody {
						c.pending = jvmScope{kind: 'c', name: c.class}
					}
					continue
				}
				name := t[i+1].s
				kind := map[string]string{"class": "class", "interface": "interface", "object": "object", "enum": "enum", "record": "class", "struct": "struct"}[tk.s]
				if tk.s == "interface" && i > 0 && t[i-1].k == 'a' {
					continue // @interface
				}
				if inClassBody {
					add(c.class+"."+name, kind, tk.line, exported)
				} else {
					add(name, kind, tk.line, exported)
				}
				c.pending = jvmScope{kind: 'c', name: name, prefix: classPrefix(t, i)}
				i++
			case "fun":
				name, line, j := funName(t, i)
				if name == "" {
					continue
				}
				if inClassBody {
					name = c.class + "." + name
					add(name, "method", line, exported)
				} else {
					add(name, "function", line, exported)
				}
				c.pending = jvmScope{kind: 'f', name: name}
				i = j
			case "val", "var", "typealias", "const":
				if tk.s == "const" {
					continue
				}
				if i+1 < len(t) && t[i+1].k == 'i' && (i+2 >= len(t) || t[i+2].s != ".") {
					kind := "var"
					if tk.s == "typealias" {
						kind = "type"
					}
					if inClassBody {
						add(c.class+"."+t[i+1].s, "property", tk.line, exported)
					} else if top && !f.java {
						add(t[i+1].s, kind, tk.line, exported)
					}
				}
			default:
				// Java: tipo nome( … ) { → método
				if f.java && inClassBody && i+1 < len(t) && t[i+1].s == "(" && !jvmKeywords[tk.s] && i > 0 &&
					(t[i-1].k == 'i' && !jvmKeywords[t[i-1].s] || t[i-1].s == ">" || t[i-1].s == "]") && tk.s != shortName(c.class) {
					if close := matchParen(t, i+1); close > 0 && close+1 < len(t) && (t[close+1].s == "{" || t[close+1].s == "throws" || t[close+1].s == "=>" || t[close+1].s == "where") {
						name := c.class + "." + tk.s
						add(name, "method", tk.line, exported)
						c.pending = jvmScope{kind: 'f', name: name}
					}
				}
			}
		}
	}
	return out
}

func shortName(s string) string { return s[strings.LastIndexByte(s, '.')+1:] }

// hasMod procura um modificador (private, public…) logo antes da declaração.
func hasMod(t []tok, i int, mod string) bool {
	for j := i - 1; j >= 0 && j >= i-8; j-- {
		if t[j].k == 'i' && t[j].s == mod {
			return true
		}
		if t[j].k == 'p' && (t[j].s == ";" || t[j].s == "{" || t[j].s == "}") {
			return false
		}
	}
	return false
}

// funName lê o nome depois de fun (pula <T> e o tipo receptor de extensões).
func funName(t []tok, i int) (string, int, int) {
	j := i + 1
	if j < len(t) && t[j].s == "<" {
		j = skipAngle(t, j)
	}
	name, line := "", 0
	for j < len(t) {
		if t[j].k == 'i' {
			name, line = t[j].s, t[j].line
			j++
			if j < len(t) && t[j].s == "<" {
				j = skipAngle(t, j)
			}
			if j < len(t) && (t[j].s == "." || t[j].s == "?") {
				if t[j].s == "?" {
					j++
				}
				j++
				continue
			}
			break
		}
		break
	}
	if j >= len(t) || t[j].s != "(" {
		return "", 0, i
	}
	return name, line, j - 1
}

func skipAngle(t []tok, j int) int {
	d := 0
	for ; j < len(t); j++ {
		switch t[j].s {
		case "<":
			d++
		case ">":
			d--
			if d == 0 {
				return j + 1
			}
		case "{", "}", ";":
			return j
		}
	}
	return j
}

func matchParen(t []tok, i int) int {
	d := 0
	for j := i; j < len(t); j++ {
		switch t[j].s {
		case "(":
			d++
		case ")":
			d--
			if d == 0 {
				return j
			}
		case "{", "}", ";":
			if t[j].k == 'p' {
				return -1
			}
		}
	}
	return -1
}

// annotation devolve o primeiro texto entre os parênteses da anotação em i.
func annArg(t []tok, i int) (string, bool) {
	if i+1 >= len(t) || t[i+1].s != "(" {
		return "", false
	}
	end := matchParen(t, i+1)
	for j := i + 2; j > 0 && j < end; j++ {
		if t[j].k == 's' {
			return t[j].s, true
		}
	}
	return "", false
}

// classPrefix: @RequestMapping("/x") antes da classe vira prefixo das rotas.
func classPrefix(t []tok, i int) string {
	for j := i - 1; j >= 0 && j >= i-40; j-- {
		if t[j].k == 'p' && (t[j].s == "}" || t[j].s == ";" || t[j].s == "{") {
			break
		}
		if t[j].k == 'a' && (t[j].s == "RequestMapping" || t[j].s == "Route") {
			if p, ok := annArg(t, j); ok {
				return p
			}
		}
	}
	return ""
}

// uses percorre o arquivo de novo emitindo usos, rotas e acessos a dados.
func (f *jvmFile) uses(idx *jvmIndex, g *model.Graph, sets map[string]string) {
	t := f.toks
	own := idx.byPkg[f.pkg]
	alias := map[string]string{} // nome importado → pacote.Nome
	var stars []string
	for _, im := range f.imports {
		if im.star {
			stars = append(stars, im.path)
		} else {
			alias[im.alias] = im.path
		}
	}
	// lookup devolve a declaração de um nome simples visto neste arquivo.
	lookup := func(name string) (*jvmDecl, string) {
		if fq, ok := alias[name]; ok {
			k := strings.LastIndexByte(fq, '.')
			if k > 0 {
				if d := idx.byPkg[fq[:k]][fq[k+1:]]; d != nil {
					return d, fq[:k]
				}
				// import de membro: pacote.Classe.membro
				if k2 := strings.LastIndexByte(fq[:k], '.'); k2 > 0 {
					if d := idx.byPkg[fq[:k2]][fq[k2+1:]]; d != nil {
						return d, fq[:k2]
					}
				}
			}
			return nil, ""
		}
		if d := own[name]; d != nil && !strings.Contains(name, ".") {
			return d, f.pkg
		}
		for _, p := range stars {
			if d := idx.byPkg[p][name]; d != nil {
				return d, p
			}
		}
		return nil, ""
	}
	vtypes := jvmVarTypes(t)
	ref := func(line int, from string, d *jvmDecl, kind string) {
		g.Refs = append(g.Refs, model.Ref{File: f.rel, Line: line, From: from, ToFile: d.file, To: d.name, Kind: kind})
	}
	kindAt := func(i int) string {
		if i > 0 && t[i-1].s == "new" {
			return "new"
		}
		if i+1 < len(t) && t[i+1].s == "(" {
			return "call"
		}
		return "ref"
	}
	// imports viram usos do tipo import
	for _, im := range f.imports {
		if im.star {
			continue
		}
		k := strings.LastIndexByte(im.path, '.')
		if k <= 0 {
			continue
		}
		if d := idx.byPkg[im.path[:k]][im.path[k+1:]]; d != nil {
			line := 1
			for _, tk := range t {
				if tk.k == 'i' && tk.s == im.path[k+1:] {
					line = tk.line
					break
				}
			}
			ref(line, "<module>", d, "import")
		}
	}
	c := &jvmCtx{}
	c.refresh()
	var annRoute *model.Route // rota de anotação esperando o método
	annAuth, annAnon := false, false
	guard := func() string {
		switch {
		case annAnon:
			return "none"
		case annAuth || c.auth == 'y':
			return "Authorize"
		case c.auth == 'n':
			return "none"
		case f.cs && !f.globalAuth:
			return "none"
		}
		return ""
	}
	var annCall *model.RouteCall
	inHeader := true
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if tk.k == 'i' && f.cs && tk.s == "namespace" && c.topLevel() {
			c.pending = jvmScope{kind: 'n'}
			_, j := dotted(t, i+1)
			i = j - 1
			continue
		}
		if tk.k == 'i' && f.cs && tk.s == "using" && c.topLevel() {
			for i < len(t) && t[i].s != ";" {
				i++
			}
			continue
		}
		if inHeader && tk.k == 'i' && (tk.s == "package" || tk.s == "import") {
			for i < len(t) && !(t[i].line > tk.line) {
				i++
			}
			i--
			continue
		}
		if tk.k == 'i' && tk.s != "package" && tk.s != "import" {
			inHeader = false
		}
		switch {
		case tk.k == 'p' && tk.s == "(":
			c.depth++
		case tk.k == 'p' && tk.s == ")":
			c.depth--
		case tk.k == 'p' && tk.s == "{":
			s := c.pending
			if s.kind == 0 {
				s.kind = 'b'
			}
			c.stack = append(c.stack, s)
			c.pending = jvmScope{}
			c.refresh()
		case tk.k == 'p' && tk.s == "}":
			if len(c.stack) > 0 {
				c.stack = c.stack[:len(c.stack)-1]
			}
			c.refresh()
		case tk.k == 'p' && tk.s == ";":
			c.pending = jvmScope{}
			if c.expr != "" && c.depth == 0 {
				c.expr = ""
				c.refresh()
			}
		case tk.k == 'a':
			switch tk.s {
			case "Authorize", "PreAuthorize", "Secured", "RolesAllowed", "PostAuthorize":
				annAuth = true
			case "AllowAnonymous", "PermitAll":
				annAnon = true
			}
			if m, ok := springMap[tk.s]; ok {
				if p, ok := annArg(t, i); ok || tk.s != "RequestMapping" {
					if m == "" {
						m = "ANY"
						if strings.Contains(joinToks(t, i, 12), "RequestMethod.") {
							for j := i; j < i+20 && j < len(t); j++ {
								if t[j].s == "RequestMethod" && j+2 < len(t) {
									m = t[j+2].s
									break
								}
							}
						}
					}
					annRoute = &model.Route{File: f.rel, Method: m, Path: p, Line: tk.line}
				}
			} else if m, ok := retrofitMap[tk.s]; ok {
				if p, ok := annArg(t, i); ok {
					annCall = &model.RouteCall{File: f.rel, Line: tk.line, Method: m, Path: "/" + strings.TrimLeft(p, "/")}
				}
			} else if tk.s == "Query" {
				if q, ok := annArg(t, i); ok && looksSQL.MatchString(q) {
					sqlUse(g, f.rel, tk.line, c.from, q, "Room")
				}
			}
		case tk.k == 's':
			inAnn := i > 1 && t[i-2].k == 'a' || i > 3 && t[i-4].k == 'a' // @X("…") ou @X(value = "…")
			// texto que declara rota (Ktor get/route) não é chamada
			declRoute := i > 1 && t[i-1].s == "(" && t[i-2].k == 'i' && ((httpVerb[t[i-2].s] != "" || t[i-2].s == "route") && !(i > 2 && t[i-3].s == ".") || minimalAPI[t[i-2].s] != "")
			if c.depth > 0 && !inAnn && !declRoute {
				if p, ok := apiRoute(tk.s); ok {
					g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: f.rel, Line: tk.line, From: c.from, Path: p})
				}
			}
			if !inAnn && looksSQL.MatchString(tk.s) {
				sqlUse(g, f.rel, tk.line, c.from, tk.s, "")
			}
		case tk.k == 'i':
			inClassBody := len(c.stack) > 0 && c.stack[len(c.stack)-1].kind == 'c'
			if f.cs {
				f.csExtras(t, i, c.from, c.prefix, g, sets, lookup)
			}
			// declarações: abrem o escopo e recebem rota de anotação
			if c.depth == 0 && (c.topLevel() || inClassBody) {
				switch tk.s {
				case "class", "interface", "object", "enum", "record", "struct":
					if i+1 < len(t) && t[i+1].k == 'i' && !(i > 0 && (t[i-1].s == "." || t[i-1].s == "::")) && !(tk.s == "enum" && t[i+1].s == "class") {
						pre := classPrefix(t, i)
						if strings.Contains(pre, "[controller]") { // ASP.NET: [Route("api/[controller]")]
							pre = strings.ReplaceAll(pre, "[controller]", strings.ToLower(strings.TrimSuffix(t[i+1].s, "Controller")))
						}
						if f.cs && pre != "" && !strings.HasPrefix(pre, "/") {
							pre = "/" + pre
						}
						c.pending = jvmScope{kind: 'c', name: t[i+1].s, prefix: pre}
						if annAuth {
							c.pending.auth = 'y'
						} else if annAnon {
							c.pending.auth = 'n'
						}
						annRoute, annCall = nil, nil
						annAuth, annAnon = false, false
						i++
						continue
					}
					if tk.s == "object" && i > 0 && t[i-1].s == "companion" && inClassBody {
						c.pending = jvmScope{kind: 'c', name: c.class}
					}
				case "fun":
					if name, _, j := funName(t, i); name != "" {
						full := name
						if inClassBody {
							full = c.class + "." + name
						}
						c.pending = jvmScope{kind: 'f', name: full}
						c.from = full
						if annRoute != nil {
							annRoute.Path = joinPath(c.prefix, annRoute.Path)
							annRoute.HandlerFile, annRoute.Handler = f.rel, full
							annRoute.Guards = guard()
							g.Routes = append(g.Routes, *annRoute)
						}
						if annCall != nil {
							annCall.From = full
							g.RouteCalls = append(g.RouteCalls, *annCall)
						}
						annRoute, annCall = nil, nil
						annAuth, annAnon = false, false
						i = j
						continue
					}
				default:
					if f.java && inClassBody && i+1 < len(t) && t[i+1].s == "(" && !jvmKeywords[tk.s] && i > 0 &&
						(t[i-1].k == 'i' && !jvmKeywords[t[i-1].s] || t[i-1].s == ">" || t[i-1].s == "]") {
						if close := matchParen(t, i+1); close > 0 && close+1 < len(t) && (t[close+1].s == "{" || t[close+1].s == "throws" || t[close+1].s == ";" || t[close+1].s == "=>" || t[close+1].s == "where") {
							full := c.class + "." + tk.s
							if t[close+1].s == "=>" {
								c.expr = full
								c.refresh()
							} else if t[close+1].s != ";" {
								c.pending = jvmScope{kind: 'f', name: full}
							}
							if annRoute != nil {
								annRoute.Path = joinPath(c.prefix, annRoute.Path)
								annRoute.HandlerFile, annRoute.Handler = f.rel, full
								annRoute.Guards = guard()
								g.Routes = append(g.Routes, *annRoute)
							}
							if annCall != nil {
								annCall.From = full
								g.RouteCalls = append(g.RouteCalls, *annCall)
							}
							annRoute, annCall = nil, nil
							annAuth, annAnon = false, false
							continue
						}
					}
				}
			}
			// Ktor: get("/x") { … } e route("/x") { … }
			if i+3 < len(t) && t[i+1].s == "(" && t[i+2].k == 's' && t[i+3].s == ")" && i+4 < len(t) && t[i+4].s == "{" && (i == 0 || t[i-1].s != ".") {
				if m, ok := httpVerb[tk.s]; ok {
					g.Routes = append(g.Routes, model.Route{File: f.rel, Method: m, Path: joinPath(c.prefix, t[i+2].s), Line: tk.line, HandlerFile: f.rel, Handler: c.from})
				} else if tk.s == "route" {
					c.pending = jvmScope{kind: 'r', prefix: t[i+2].s}
				}
			}
			// Supabase: from("tabela").select(), rpc("fn"), storage.from("bucket")
			if (tk.s == "from" || tk.s == "rpc") && i+3 < len(t) && t[i+1].s == "(" && t[i+2].k == 's' && t[i+3].s == ")" {
				typ := "table"
				recv := ""
				if i > 1 && t[i-1].s == "." {
					recv = t[i-2].s
				}
				if tk.s == "rpc" {
					typ = "rpc"
				} else if recv == "storage" {
					typ = "bucket"
				}
				if recv != "" || tk.s == "rpc" {
					var ops []string
					for j := i + 4; j < len(t) && j < i+40 && t[j].s != ";"; j++ {
						if t[j].k == 'i' && j > 0 && t[j-1].s == "." && (tableOps[t[j].s] || typ == "bucket") {
							ops = append(ops, t[j].s)
							break
						}
					}
					g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: tk.line, From: c.from, Type: typ, Name: t[i+2].s, Ops: ops, Client: recv})
				}
			}
			// usos de nomes conhecidos
			if i > 0 && (t[i-1].s == "." || t[i-1].s == "::") {
				// Classe.membro, this.membro e variavel.membro (pelo tipo declarado)
				if i > 1 && t[i-2].k == 'i' {
					q := t[i-2].s
					cls := ""
					if d, _ := lookup(q); d == nil && vtypes[q] != "" {
						q = vtypes[q]
					}
					if q == "this" && c.class != "" {
						cls = c.class
					} else if d, _ := lookup(q); d != nil && !strings.Contains(d.name, ".") {
						cls = d.name
					}
					if cls != "" {
						if d := idx.byPkg[f.pkg][cls+"."+tk.s]; d != nil && cls == c.class {
							ref(tk.line, c.from, d, kindAt(i))
						} else if dq, p := lookup(q); dq != nil {
							if d := idx.byPkg[p][cls+"."+tk.s]; d != nil {
								ref(tk.line, c.from, d, kindAt(i))
							}
						} else if d := idx.byPkg[f.pkg][cls+"."+tk.s]; d != nil {
							ref(tk.line, c.from, d, kindAt(i))
						}
					}
				}
				continue
			}
			if jvmKeywords[tk.s] || isDeclName(t, i) {
				continue
			}
			// membro da própria classe chamado sem this
			if c.class != "" && i+1 < len(t) && t[i+1].s == "(" {
				if d := own[c.class+"."+tk.s]; d != nil {
					ref(tk.line, c.from, d, "call")
					continue
				}
			}
			if d, _ := lookup(tk.s); d != nil {
				if d.file == f.rel && d.line == tk.line {
					continue
				}
				ref(tk.line, c.from, d, kindAt(i))
			}
		}
	}
}

// isDeclName diz se o identificador em i é o nome sendo declarado.
func isDeclName(t []tok, i int) bool {
	if i == 0 {
		return false
	}
	switch t[i-1].s {
	case "class", "interface", "object", "fun", "val", "var", "typealias", "enum", "record":
		return true
	}
	return false
}

func joinToks(t []tok, i, n int) string {
	var b strings.Builder
	for j := i; j < len(t) && j < i+n; j++ {
		b.WriteString(t[j].s)
	}
	return b.String()
}

// jvmVarTypes acha o tipo declarado das variáveis do arquivo (sem escopo:
// o último vence): val x: Tipo, val x = Tipo(…), Tipo x = …, var x = new Tipo(…),
// parâmetros (x: Tipo) e (Tipo x).
func jvmVarTypes(t []tok) map[string]string {
	vt := map[string]string{}
	up := func(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }
	for i := 1; i+2 < len(t); i++ {
		tk := t[i]
		if tk.k != 'i' || up(tk.s) || jvmKeywords[tk.s] {
			continue
		}
		prev, next := t[i-1], t[i+1]
		switch {
		// Kotlin/Scala: val x: Tipo / (x: Tipo)
		case next.s == ":" && t[i+2].k == 'i' && up(t[i+2].s) && (prev.s == "val" || prev.s == "var" || prev.s == "(" || prev.s == ","):
			vt[tk.s] = t[i+2].s
		// val x = Tipo(…) / var x = new Tipo(…)
		case next.s == "=" && (prev.s == "val" || prev.s == "var" || prev.s == "let"):
			j := i + 2
			if t[j].s == "new" && j+1 < len(t) {
				j++
			}
			if t[j].k == 'i' && up(t[j].s) && j+1 < len(t) && (t[j+1].s == "(" || t[j+1].s == "{" || t[j+1].s == "<") {
				vt[tk.s] = t[j].s
			}
		// Java/C#: Tipo x = … / Tipo x; / (Tipo x, …)
		case prev.k == 'i' && up(prev.s) && (next.s == "=" || next.s == ";" || next.s == "," || next.s == ")"):
			if !(i > 1 && t[i-2].s == ".") {
				vt[tk.s] = prev.s
			}
		}
	}
	return vt
}
