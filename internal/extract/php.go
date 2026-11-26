package extract

// PHP e Laravel sem rodar o PHP: namespaces e use (com alias), classes,
// interfaces, traits e enums com seus métodos, funções soltas; usos por
// new X, X::metodo(), $this->metodo(), self::/static::, X::class e funções.
// Rotas do Laravel (Route::get, grupos com prefix, resource/apiResource,
// routes/api.php com /api), Eloquent (Modelo::where/create… vira tabela),
// DB::table('x'), SQL em texto e chamadas /api/... .

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

var phpSkip = map[string]bool{"vendor": true, "node_modules": true, "storage": true, "bootstrap": true, "public": true, "cache": true}

type phpFile struct {
	rel, ns string
	toks    []tok
	lines   int
	uses    map[string]string // alias → Nome\Completo
}

type phpDecl struct {
	file, name, kind string
	line             int
	exported         bool
	fqn              string // Ns\Classe (só classes)
	table            string // modelo Eloquent: tabela
}

// phpNormalize deixa o PHP no formato do leitor de tokens: 'texto' vira
// "texto" (podendo ter várias linhas), # comentário some, <?php some.
func phpNormalize(src []byte) []byte {
	var b strings.Builder
	n := len(src)
	for i := 0; i < n; i++ {
		c := src[i]
		switch {
		case c == '<' && strings.HasPrefix(string(src[i:min(n, i+5)]), "<?php"):
			i += 4
		case c == '?' && i+1 < n && src[i+1] == '>':
			i++
		case c == '#' && !(i+1 < n && src[i+1] == '['):
			for i < n && src[i] != '\n' {
				i++
			}
			if i < n {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < n && (src[i+1] == '/' || src[i+1] == '*'):
			// comentários: copia como estão (o leitor de tokens pula)
			end := "\n"
			if src[i+1] == '*' {
				end = "*/"
			}
			j := strings.Index(string(src[i+2:]), end)
			if j < 0 {
				j = n - i - 2
			}
			stop := i + 2 + j + len(end)
			if stop > n {
				stop = n
			}
			b.Write(src[i:stop])
			i = stop - 1
		case c == '"' || c == '\'':
			j := i + 1
			var s strings.Builder
			for j < n && src[j] != c {
				if src[j] == '\\' && j+1 < n {
					if src[j+1] == c || src[j+1] == '\\' {
						s.WriteByte(src[j+1])
						j += 2
						continue
					}
				}
				s.WriteByte(src[j])
				j++
			}
			v := s.String()
			// mantém as quebras de linha para as linhas baterem
			b.WriteByte('"')
			b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v))
			b.WriteByte('"')
			i = j
		default:
			b.WriteByte(c)
		}
	}
	return []byte(b.String())
}

func phpFiles(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || phpSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".php") && !strings.HasSuffix(p, ".blade.php") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// phpName lê A\B\C a partir de i (com \ na frente ou não).
func phpName(t []tok, i int) (string, int) {
	var parts []string
	lead := false
	if i < len(t) && t[i].s == `\` {
		lead = true
		i++
	}
	for i < len(t) && t[i].k == 'i' {
		parts = append(parts, t[i].s)
		i++
		if i < len(t) && t[i].s == `\` && i+1 < len(t) && t[i+1].k == 'i' {
			i++
			continue
		}
		break
	}
	n := strings.Join(parts, `\`)
	if lead {
		n = `\` + n
	}
	return n, i
}

var snakeRe = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// eloquentTable: tabela padrão de um modelo (Pedido → pedidos, OrderItem → order_items).
func eloquentTable(class string) string {
	s := strings.ToLower(snakeRe.ReplaceAllString(class, "${1}_${2}"))
	switch {
	case strings.HasSuffix(s, "y") && !strings.HasSuffix(s, "ay") && !strings.HasSuffix(s, "ey") && !strings.HasSuffix(s, "oy"):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s") || strings.HasSuffix(s, "x") || strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}

var eloquentOps = map[string]string{"create": "insert", "insert": "insert", "firstOrCreate": "insert", "updateOrCreate": "upsert", "upsert": "upsert",
	"update": "update", "increment": "update", "decrement": "update", "delete": "delete", "destroy": "delete", "forceDelete": "delete", "truncate": "delete"}

// PHP extrai o código PHP do projeto. Com only, só esses arquivos entram no grafo.
func PHP(root string, g *model.Graph, only []string) error {
	var files []*phpFile
	for _, p := range phpFiles(root) {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		f := &phpFile{rel: filepath.ToSlash(rel), toks: lex(phpNormalize(src), true), lines: strings.Count(string(src), "\n") + 1, uses: map[string]string{}}
		files = append(files, f)
	}
	// 1ª passada: namespaces, use e declarações
	byFQN := map[string]*phpDecl{}   // Ns\Classe → classe
	members := map[string]*phpDecl{} // Ns\Classe::metodo → método
	funcs := map[string]*phpDecl{}   // Ns\funcao → função
	var all [][]*phpDecl
	for _, f := range files {
		var ds []*phpDecl
		t := f.toks
		depth := 0
		class := ""
		classDepth := -1
		for i := 0; i < len(t); i++ {
			tk := t[i]
			switch {
			case tk.s == "{" && tk.k == 'p':
				depth++
			case tk.s == "}" && tk.k == 'p':
				depth--
				if depth == classDepth {
					class, classDepth = "", -1
				}
			case tk.k == 'i' && tk.s == "namespace" && depth == 0:
				f.ns, _ = phpName(t, i+1)
			case tk.k == 'i' && tk.s == "use" && depth == 0 && class == "":
				j := i + 1
				if j < len(t) && (t[j].s == "function" || t[j].s == "const") {
					j++
				}
				for j < len(t) && t[j].s != ";" {
					name, k := phpName(t, j)
					name = strings.TrimPrefix(name, `\`)
					if k < len(t) && t[k].s == "{" { // use A\{B, C as D}
						k++
						for k < len(t) && t[k].s != "}" {
							sub, k2 := phpName(t, k)
							alias := sub[strings.LastIndexByte(sub, '\\')+1:]
							if k2+1 < len(t) && t[k2].s == "as" {
								alias, k2 = t[k2+1].s, k2+2
							}
							f.uses[alias] = name + `\` + sub
							k = k2
							if k < len(t) && t[k].s == "," {
								k++
							}
						}
						j = k + 1
						continue
					}
					alias := name[strings.LastIndexByte(name, '\\')+1:]
					if k+1 < len(t) && t[k].s == "as" {
						alias, k = t[k+1].s, k+2
					}
					if name != "" {
						f.uses[alias] = name
					}
					j = k
					if j < len(t) && t[j].s == "," {
						j++
					}
					if k == j && t[j].s != ";" && t[j].s != "," {
						j++
					}
				}
				i = j
			case tk.k == 'i' && (tk.s == "class" || tk.s == "interface" || tk.s == "trait" || tk.s == "enum") && i+1 < len(t) && t[i+1].k == 'i' && !(i > 0 && t[i-1].s == "::"):
				name := t[i+1].s
				fq := strings.TrimPrefix(f.ns+`\`+name, `\`)
				d := &phpDecl{file: f.rel, name: name, kind: map[string]string{"class": "class", "interface": "interface", "trait": "trait", "enum": "enum"}[tk.s], line: tk.line, exported: true, fqn: fq}
				// modelo Eloquent: extends Model; $table manda no nome
				for j := i + 2; j < len(t) && j < i+10 && t[j].s != "{"; j++ {
					if t[j].s == "Model" || t[j].s == "Authenticatable" || t[j].s == "Pivot" {
						d.table = eloquentTable(name)
					}
				}
				ds = append(ds, d)
				byFQN[fq] = d
				class, classDepth = name, depth
			case tk.k == 'i' && tk.s == "protected" && class != "" && i+3 < len(t) && t[i+1].s == "$table" && t[i+2].s == "=" && t[i+3].k == 's':
				if d := byFQN[strings.TrimPrefix(f.ns+`\`+class, `\`)]; d != nil {
					d.table = t[i+3].s
				}
			case tk.k == 'i' && tk.s == "function" && i+1 < len(t) && t[i+1].k == 'i' && i+2 < len(t) && t[i+2].s == "(":
				name := t[i+1].s
				if class != "" && depth == classDepth+1 {
					pub := !phpHasMod(t, i, "private") && !phpHasMod(t, i, "protected")
					d := &phpDecl{file: f.rel, name: class + "." + name, kind: "method", line: tk.line, exported: pub}
					ds = append(ds, d)
					members[strings.TrimPrefix(f.ns+`\`+class, `\`)+"::"+name] = d
				} else if class == "" && depth == 0 {
					d := &phpDecl{file: f.rel, name: name, kind: "function", line: tk.line, exported: true}
					ds = append(ds, d)
					funcs[strings.TrimPrefix(f.ns+`\`+name, `\`)] = d
				}
			}
		}
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
		f.phpUses(g, byFQN, members, funcs)
	}
	return nil
}

func phpHasMod(t []tok, i int, mod string) bool {
	for j := i - 1; j >= 0 && j >= i-5; j-- {
		if t[j].s == mod {
			return true
		}
		if t[j].s == ";" || t[j].s == "{" || t[j].s == "}" {
			return false
		}
	}
	return false
}

var laravelVerbs = map[string]string{"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "delete": "DELETE", "options": "OPTIONS", "any": "ANY", "match": "ANY"}

func (f *phpFile) phpUses(g *model.Graph, byFQN map[string]*phpDecl, members map[string]*phpDecl, funcs map[string]*phpDecl) {
	t := f.toks
	// resolve um nome de classe escrito no código
	class := func(name string) *phpDecl {
		switch {
		case strings.HasPrefix(name, `\`):
			return byFQN[name[1:]]
		case f.uses[name] != "":
			return byFQN[f.uses[name]]
		}
		first, rest, _ := strings.Cut(name, `\`)
		if u := f.uses[first]; u != "" && rest != "" {
			return byFQN[u+`\`+rest]
		}
		if d := byFQN[strings.TrimPrefix(f.ns+`\`+name, `\`)]; d != nil {
			return d
		}
		return byFQN[name]
	}
	ref := func(line int, from string, d *phpDecl, kind string) {
		g.Refs = append(g.Refs, model.Ref{File: f.rel, Line: line, From: from, ToFile: d.file, To: d.name, Kind: kind})
	}
	method := func(c *phpDecl, m string) *phpDecl { return members[c.fqn+"::"+m] }

	depth := 0
	type scope struct {
		depth  int
		name   string // classe ou função
		cls    bool
		prefix string
		mw     string // middleware do grupo
	}
	var stack []scope
	cur, from, prefix, mw := "", "<module>", "", ""
	if strings.HasSuffix(f.rel, "routes/api.php") {
		prefix = "/api"
	}
	base := prefix
	refresh := func() {
		cur, from, prefix, mw = "", "<module>", base, ""
		for _, s := range stack {
			if s.mw != "" {
				mw = strings.Trim(mw+","+s.mw, ",")
			}
			if s.cls {
				cur, from = s.name, s.name
			} else if s.name != "" {
				from = s.name
			}
			if s.prefix != "" {
				prefix = joinPath(prefix, s.prefix)
			}
		}
	}
	var pending scope
	pendingPrefix, pendingMW := "", ""
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if tk.k == 'p' {
			switch tk.s {
			case "{":
				depth++
				s := pending
				s.depth = depth
				if pendingPrefix != "" {
					s.prefix, pendingPrefix = pendingPrefix, ""
				}
				if pendingMW != "" {
					s.mw, pendingMW = pendingMW, ""
				}
				stack = append(stack, s)
				pending = scope{}
				refresh()
			case "}":
				depth--
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				refresh()
			case ";":
				pendingMW = "" // ->middleware() de uma rota só não passa para o próximo grupo
				pending = scope{}
				refresh()
			}
			continue
		}
		if tk.k == 's' {
			if looksSQL.MatchString(tk.s) {
				sqlUse(g, f.rel, tk.line, from, tk.s, "")
			}
			if i > 0 && t[i-1].s == "(" && !(i > 2 && t[i-3].s == "::" && t[i-4].s == "Route") {
				if p, ok := apiRoute(tk.s); ok {
					g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: f.rel, Line: tk.line, From: from, Path: p})
				}
			}
			continue
		}
		if tk.k != 'i' {
			continue
		}
		switch tk.s {
		case "class", "interface", "trait", "enum":
			if i+1 < len(t) && t[i+1].k == 'i' && !(i > 0 && t[i-1].s == "::") {
				pending = scope{name: t[i+1].s, cls: true}
				i++
				continue
			}
		case "function":
			if i+1 < len(t) && t[i+1].k == 'i' {
				name := t[i+1].s
				if cur != "" {
					name = cur + "." + name
				}
				pending = scope{name: name}
				from = name // a assinatura (tipos dos parâmetros) já é do método
				i++
				continue
			}
		case "namespace", "use":
			if depth == 0 || tk.s == "use" && cur != "" && len(stack) == 1 {
				for i < len(t) && t[i].s != ";" && t[i].s != "{" {
					i++
				}
				if i < len(t) && t[i].s == "{" && tk.s == "use" {
					for i < len(t) && t[i].s != "}" {
						i++
					}
				}
				continue
			}
		case "Route":
			if i+3 < len(t) && t[i+1].s == "::" {
				f.laravelRoute(g, t, i, prefix, from, mw, class, method, &pendingPrefix, &pendingMW)
			}
		case "DB":
			// DB::table('x')->where(...)->update(...)
			if i+4 < len(t) && t[i+1].s == "::" && t[i+2].s == "table" && t[i+3].s == "(" && t[i+4].k == 's' {
				ops := []string{phpChainOp(t, i+5, "select")}
				g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: tk.line, From: from, Type: "table", Name: t[i+4].s, Ops: ops, Client: "DB"})
			}
		}
		if i > 0 && (t[i-1].s == "->" || t[i-1].s == "::" || t[i-1].s == `\` || t[i-1].s == "function" || t[i-1].s == "class") {
			// Route::middleware('auth')->prefix('admin')->group(…)
			if t[i-1].s == "->" && tk.s == "prefix" && i+2 < len(t) && t[i+1].s == "(" && t[i+2].k == 's' {
				pendingPrefix = t[i+2].s
			}
			if t[i-1].s == "->" && tk.s == "middleware" && i+1 < len(t) && t[i+1].s == "(" {
				if m := phpStrings(t, i+1); m != "" {
					pendingMW = strings.Trim(pendingMW+","+m, ",")
				}
			}
			// $this->metodo()
			if t[i-1].s == "->" && i > 1 && t[i-2].s == "$this" && cur != "" && i+1 < len(t) && t[i+1].s == "(" {
				if c := class(cur); c != nil {
					if m := method(c, tk.s); m != nil {
						ref(tk.line, from, m, "call")
					}
				}
			}
			continue
		}
		name, end := phpName(t, i)
		switch {
		case end < len(t) && t[end].s == "::" && end+1 < len(t):
			// X::metodo(), X::class, self::/static::
			cn := name
			if cn == "self" || cn == "static" {
				cn = cur
			}
			c := class(cn)
			if c == nil {
				i = end - 1
				continue
			}
			if name != "self" && name != "static" {
				ref(tk.line, from, c, "ref")
			}
			m := t[end+1].s
			if md := method(c, m); md != nil && end+2 < len(t) && t[end+2].s == "(" {
				ref(tk.line, from, md, "call")
			}
			if c.table != "" && end+2 < len(t) && t[end+2].s == "(" && m != "class" {
				op := eloquentOps[m]
				if op == "" {
					op = phpChainOp(t, end+3, "select")
				}
				g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: tk.line, From: from, Type: "table", Name: c.table, Ops: []string{op}, Client: c.name})
			}
			i = end
		case i > 0 && t[i-1].s == "new":
			if c := class(name); c != nil {
				ref(tk.line, from, c, "new")
			}
			i = end - 1
		case end < len(t) && t[end].s == "(":
			if d := funcs[f.uses[name]]; d != nil { // use function A\b
				ref(tk.line, from, d, "call")
			} else if d := funcs[strings.TrimPrefix(f.ns+`\`+name, `\`)]; d != nil {
				ref(tk.line, from, d, "call")
			} else if d := funcs[strings.TrimPrefix(name, `\`)]; d != nil {
				ref(tk.line, from, d, "call")
			}
			i = end - 1
		case end < len(t) && t[end].k == 'i' && strings.HasPrefix(t[end].s, "$"):
			// tipo de parâmetro: Pedido $p
			if c := class(name); c != nil {
				ref(tk.line, from, c, "type")
			}
			i = end - 1
		default:
			i = end - 1
		}
	}
}

// phpChainOp olha a cadeia ->a()->b() depois de i e devolve a operação de escrita, se houver.
func phpChainOp(t []tok, i int, def string) string {
	d := 0
	for j := i; j < len(t) && j < i+80; j++ {
		switch t[j].s {
		case "(":
			d++
		case ")":
			d--
		case ";":
			return def
		}
		if d <= 0 && j > 0 && t[j-1].s == "->" && t[j].k == 'i' {
			if op := eloquentOps[t[j].s]; op != "" {
				return op
			}
		}
	}
	return def
}

// laravelRoute lê Route::get('/x', [Controller::class, 'metodo']) e companhia.
func (f *phpFile) laravelRoute(g *model.Graph, t []tok, i int, prefix, from, mw string, class func(string) *phpDecl, method func(*phpDecl, string) *phpDecl, pendingPrefix, pendingMW *string) {
	verb := t[i+2].s
	line := t[i].line
	// guardas da rota: middleware do grupo + ->middleware(…) no fim do comando
	guards := func() string {
		gs := mw
		for j := i + 3; j < len(t) && t[j].s != ";"; j++ {
			if t[j].s == "middleware" && t[j-1].s == "->" {
				if m := phpStrings(t, j+1); m != "" {
					gs = strings.Trim(gs+","+m, ",")
				}
			}
		}
		if gs == "" {
			return "none"
		}
		return gs
	}
	// Route::middleware('auth')->group(…)
	if verb == "middleware" && i+3 < len(t) && t[i+3].s == "(" {
		*pendingMW = strings.Trim(*pendingMW+","+phpStrings(t, i+3), ",")
		return
	}
	// Route::prefix('x')->group(function () { … }) e Route::prefix('x')->middleware(…)->group(…)
	if verb == "prefix" && i+4 < len(t) && t[i+3].s == "(" && t[i+4].k == 's' {
		*pendingPrefix = t[i+4].s
		return
	}
	// Route::group(['prefix' => 'x'], function () { … })
	if verb == "group" && i+4 < len(t) && t[i+4].s == "[" {
		for j := i + 5; j+2 < len(t) && t[j].s != "]"; j++ {
			if t[j].k == 's' && t[j].s == "prefix" && t[j+1].s == "=" && t[j+2].s == ">" && j+3 < len(t) && t[j+3].k == 's' {
				*pendingPrefix = t[j+3].s
			}
			if t[j].k == 's' && t[j].s == "middleware" && t[j+1].s == "=" && t[j+2].s == ">" && j+3 < len(t) {
				if t[j+3].k == 's' {
					*pendingMW = t[j+3].s
				} else if t[j+3].s == "[" {
					var ms []string
					for k := j + 4; k < len(t) && t[k].s != "]"; k++ {
						if t[k].k == 's' {
							ms = append(ms, t[k].s)
						}
					}
					*pendingMW = strings.Join(ms, ",")
				}
			}
		}
		return
	}
	if verb == "resource" || verb == "apiResource" {
		if i+6 < len(t) && t[i+4].k == 's' {
			res := t[i+4].s
			cn, _ := phpName(t, i+6)
			c := class(cn)
			param := "{" + strings.TrimSuffix(res[strings.LastIndexByte(res, '/')+1:], "s") + "}"
			base := joinPath(prefix, res)
			actions := [][3]string{{"GET", "", "index"}, {"POST", "", "store"}, {"GET", "/" + param, "show"}, {"PUT", "/" + param, "update"}, {"DELETE", "/" + param, "destroy"}}
			if verb == "resource" {
				actions = append(actions, [3]string{"GET", "/create", "create"}, [3]string{"GET", "/" + param + "/edit", "edit"})
			}
			gs := guards()
			for _, a := range actions {
				r := model.Route{File: f.rel, Method: a[0], Path: base + a[1], Line: line, HandlerFile: f.rel, Handler: a[2], Guards: gs}
				if c != nil {
					r.HandlerFile, r.Handler = c.file, c.name+"."+a[2]
				}
				g.Routes = append(g.Routes, r)
			}
		}
		return
	}
	m, ok := laravelVerbs[verb]
	if !ok || i+4 >= len(t) || t[i+3].s != "(" {
		return
	}
	j := i + 4
	if verb == "match" { // Route::match(['get','post'], '/x', …)
		for j < len(t) && t[j].s != "]" {
			j++
		}
		j += 2
	}
	if j >= len(t) || t[j].k != 's' {
		return
	}
	path := joinPath(prefix, t[j].s)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r := model.Route{File: f.rel, Method: m, Path: path, Line: line, HandlerFile: f.rel, Handler: from, Guards: guards()}
	k := j + 1
	if k < len(t) && t[k].s == "," {
		k++
	}
	switch {
	case k+5 < len(t) && t[k].s == "[":
		// [Controller::class, 'metodo']
		cn, e := phpName(t, k+1)
		if c := class(cn); c != nil && e+3 < len(t) && t[e].s == "::" && t[e+1].s == "class" && t[e+2].s == "," && t[e+3].k == 's' {
			if md := method(c, t[e+3].s); md != nil {
				r.HandlerFile, r.Handler = md.file, md.name
			} else {
				r.HandlerFile, r.Handler = c.file, c.name+"."+t[e+3].s
			}
		}
	case k < len(t) && t[k].k == 's' && strings.Contains(t[k].s, "@"):
		// 'Controller@metodo'
		cn, mn, _ := strings.Cut(t[k].s, "@")
		if c := class(cn); c != nil {
			r.HandlerFile, r.Handler = c.file, c.name+"."+mn
		}
	case k+2 < len(t) && t[k+1].s == "::" && t[k+2].s == "class":
		// controller invocável: Controller::class
		cn, _ := phpName(t, k)
		if c := class(cn); c != nil {
			r.HandlerFile, r.Handler = c.file, c.name+".__invoke"
		}
	}
	g.Routes = append(g.Routes, r)
}

// phpStrings lê os textos de uma lista de argumentos: ('auth') ou (['auth', 'verified']).
func phpStrings(t []tok, open int) string {
	var out []string
	d := 0
	for j := open; j < len(t); j++ {
		switch t[j].s {
		case "(":
			d++
		case ")":
			d--
		}
		if d == 0 {
			break
		}
		if t[j].k == 's' {
			out = append(out, t[j].s)
		}
	}
	return strings.Join(out, ",")
}
