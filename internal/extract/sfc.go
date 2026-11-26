package extract

// Componentes Vue (.vue), Svelte (.svelte) e Astro (.astro), que o compilador do
// TypeScript não lê. Roda depois do TypeScript, porque resolve os imports
// contra as declarações que ele achou: cada import vira uso, cada <Componente>
// no template vira uso de componente, cada chamada a uma função importada vira
// chamada. Também acha as telas (SvelteKit src/routes/**/+page.svelte e Astro
// src/pages/**/*.astro, Nuxt pages/**/*.vue e as rotas do Vue Router), as
// chamadas /api/... e o Supabase (from/rpc). Do Nuxt também: componentes e
// composables importados sozinhos e as rotas de server/api.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

var sfcSkip = map[string]bool{"node_modules": true, "dist": true, "build": true, ".svelte-kit": true, ".astro": true, ".vercel": true, ".netlify": true, ".output": true, ".nuxt": true}

func sfcFiles(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || sfcSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".svelte") || strings.HasSuffix(p, ".astro") || strings.HasSuffix(p, ".vue") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

var importRe = regexp.MustCompile(`(?m)^\s*import\s+(?:type\s+)?([^'"]*?)\s*from\s*['"]([^'"]+)['"]`)

// untemplate troca `textos ${x}` por "textos ${}", para o leitor de tokens.
func untemplate(src []byte) []byte {
	var b strings.Builder
	for i := 0; i < len(src); i++ {
		if src[i] == '"' { // texto em aspas duplas: copia inteiro
			j := i + 1
			for j < len(src) && src[j] != '"' && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			b.Write(src[i:min(j+1, len(src))])
			i = j
			continue
		}
		if src[i] == '\'' && (i == 0 || !isIdent(src[i-1])) {
			// 'texto' do JavaScript vira "texto" (só se fechar na mesma linha)
			j := i + 1
			for j < len(src) && src[j] != '\'' && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(src) && src[j] == '\'' {
				b.WriteByte('"')
				b.WriteString(strings.ReplaceAll(string(src[i+1:j]), `"`, `\"`))
				b.WriteByte('"')
				i = j
				continue
			}
		}
		if src[i] != '`' {
			b.WriteByte(src[i])
			continue
		}
		b.WriteByte('"')
		for i++; i < len(src) && src[i] != '`'; i++ {
			switch {
			case src[i] == '\\' && i+1 < len(src):
				i++
				b.WriteByte(src[i])
			case src[i] == '$' && i+1 < len(src) && src[i+1] == '{':
				b.WriteString("${}")
				d := 0
				for ; i < len(src); i++ {
					if src[i] == '{' {
						d++
					} else if src[i] == '}' {
						d--
						if d == 0 {
							break
						}
					}
				}
			case src[i] == '"':
				b.WriteString(`\"`)
			case src[i] == '\n':
				b.WriteByte(' ')
			default:
				b.WriteByte(src[i])
			}
		}
		b.WriteByte('"')
	}
	return []byte(b.String())
}

// sfcPage devolve a rota de uma tela de SvelteKit ou Astro.
func sfcPage(rel string) (string, bool) {
	clean := func(p string) string {
		var segs []string
		for _, s := range strings.Split(p, "/") {
			if s != "" && !(strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")) {
				segs = append(segs, s)
			}
		}
		return "/" + strings.Join(segs, "/")
	}
	if i := strings.Index(rel, "src/routes/"); i >= 0 && strings.HasSuffix(rel, "/+page.svelte") || rel == "src/routes/+page.svelte" {
		dir := strings.TrimSuffix(strings.TrimPrefix(rel[max(0, i):], "src/routes/"), "+page.svelte")
		return clean(dir), true
	}
	if strings.HasSuffix(rel, ".vue") {
		// Nuxt: pages/**/*.vue (na raiz, em app/ ou em src/)
		for _, pre := range []string{"pages/", "app/pages/", "src/pages/"} {
			if strings.HasPrefix(rel, pre) {
				p := strings.TrimSuffix(strings.TrimPrefix(rel, pre), ".vue")
				p = strings.TrimSuffix(p, "index")
				return clean(p), true
			}
		}
		return "", false
	}
	if i := strings.Index(rel, "src/pages/"); i >= 0 && strings.HasSuffix(rel, ".astro") {
		p := strings.TrimSuffix(rel[i+len("src/pages/"):], ".astro")
		p = strings.TrimSuffix(p, "index")
		return clean(p), true
	}
	return "", false
}

// SFC extrai os .svelte e .astro. g já precisa ter as declarações do TypeScript.
func SFC(root string, g *model.Graph) error {
	files := sfcFiles(root)
	if len(files) == 0 {
		return nil
	}
	decls := map[string]bool{}
	for _, d := range g.Decls {
		decls[d.File+"|"+d.Name] = true
	}
	comp := func(rel string) string {
		b := filepath.Base(rel)
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(b, ".svelte"), ".astro"), ".vue")
	}
	var rels []string
	for _, p := range files {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		rels = append(rels, rel)
		decls[rel+"|"+comp(rel)] = true
	}
	exists := func(rel string) bool { _, err := os.Stat(filepath.Join(root, rel)); return err == nil }
	nuxt := isNuxt(root)
	type target struct{ file, name string }
	// componentes registrados sozinhos (Nuxt e unplugin-vue-components):
	// components/base/Botao.vue vira <BaseBotao> (e <Botao>, se for único)
	auto := map[string]target{}
	short := map[string][]string{}
	for _, rel := range rels {
		k := strings.Index("/"+rel, "/components/")
		if k < 0 || !strings.HasSuffix(rel, ".vue") {
			continue
		}
		inside := strings.TrimSuffix(rel[k+len("components/"):], ".vue")
		dir, file := "", inside
		if j := strings.LastIndexByte(inside, '/'); j >= 0 {
			dir, file = inside[:j], inside[j+1:]
		}
		file = strings.TrimSuffix(file, "/index")
		pre, fc := camel(dir), camel(file)
		name := fc
		if pre != "" && !strings.HasPrefix(fc, pre) {
			name = pre + fc
		}
		auto[name] = target{rel, comp(rel)}
		auto["Lazy"+name] = auto[name]
		short[fc] = append(short[fc], rel)
	}
	for fc, list := range short {
		if _, ok := auto[fc]; !ok && len(list) == 1 {
			auto[fc] = target{list[0], comp(list[0])}
		}
	}
	// composables e utils do Nuxt: usados sem import
	if nuxt {
		for _, d := range g.Decls {
			if !d.Exported {
				continue
			}
			for _, dir := range []string{"composables/", "utils/"} {
				if strings.HasPrefix(d.File, dir) || strings.HasPrefix(d.File, "app/"+dir) || strings.HasPrefix(d.File, "src/"+dir) {
					auto[d.Name] = target{d.File, d.Name}
				}
			}
		}
	}
	resolve := func(from, spec string) string {
		var bases []string
		switch {
		case strings.HasPrefix(spec, "."):
			bases = []string{filepath.ToSlash(filepath.Join(filepath.Dir(from), spec))}
		case strings.HasPrefix(spec, "$lib/"):
			bases = []string{"src/lib/" + strings.TrimPrefix(spec, "$lib/")}
		case strings.HasPrefix(spec, "~~/") || strings.HasPrefix(spec, "@@/"):
			bases = []string{spec[3:]}
		case strings.HasPrefix(spec, "@/") || strings.HasPrefix(spec, "~/"):
			bases = []string{"src/" + spec[2:], "app/" + spec[2:], spec[2:]}
		default:
			return ""
		}
		for _, base := range bases {
			for _, ext := range []string{"", ".ts", ".js", ".tsx", ".jsx", ".svelte", ".astro", ".vue", "/index.ts", "/index.js"} {
				if c := base + ext; exists(c) && !isDir(filepath.Join(root, c)) {
					return c
				}
			}
		}
		return ""
	}
	for i, p := range files {
		rel := rels[i]
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		name := comp(rel)
		g.Files = append(g.Files, model.File{Path: rel, Lines: strings.Count(string(src), "\n") + 1})
		g.Decls = append(g.Decls, model.Decl{File: rel, Name: name, Kind: "component", Line: 1, Exported: true})
		if path, ok := sfcPage(rel); ok {
			g.Pages = append(g.Pages, model.Page{File: rel, Path: path, Line: 1, Component: name})
		}
		// imports: nome local → declaração
		local := map[string]target{}
		lineAt := func(off int) int { return strings.Count(string(src[:off]), "\n") + 1 }
		for _, m := range importRe.FindAllSubmatchIndex(src, -1) {
			clause, spec := string(src[m[2]:m[3]]), string(src[m[4]:m[5]])
			tf := resolve(rel, spec)
			if tf == "" {
				continue
			}
			line := lineAt(m[0])
			add := func(localName, exported string) {
				t := target{tf, exported}
				if exported == "" { // import padrão
					if strings.HasSuffix(tf, ".svelte") || strings.HasSuffix(tf, ".astro") {
						t.name = comp(tf)
					} else {
						t.name = localName
					}
				}
				if !decls[t.file+"|"+t.name] {
					return
				}
				local[localName] = t
				g.Refs = append(g.Refs, model.Ref{File: rel, Line: line, From: "<module>", ToFile: t.file, To: t.name, Kind: "import"})
			}
			clause = strings.TrimSpace(clause)
			if j := strings.IndexByte(clause, '{'); j >= 0 {
				if def := strings.Trim(strings.TrimSpace(clause[:j]), ","); def != "" {
					add(strings.TrimSpace(def), "")
				}
				inner := clause[j+1:]
				if k := strings.IndexByte(inner, '}'); k >= 0 {
					inner = inner[:k]
				}
				for _, part := range strings.Split(inner, ",") {
					part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
					if part == "" {
						continue
					}
					orig, alias := part, part
					if k := strings.Index(part, " as "); k > 0 {
						orig, alias = strings.TrimSpace(part[:k]), strings.TrimSpace(part[k+4:])
					}
					add(alias, orig)
				}
			} else if !strings.HasPrefix(clause, "*") && clause != "" {
				add(clause, "")
			}
		}
		// usos no script e no template (as linhas de import já viraram uso)
		importLine := map[int]bool{}
		for k, l := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "import ") {
				importLine[k+1] = true
			}
		}
		t := lex(untemplate(src), false)
		for j := 0; j < len(t); j++ {
			tk := t[j]
			if tk.k == 's' && j > 0 && t[j-1].s == "=" && strings.HasSuffix(rel, ".vue") && tk.s != "" {
				// @click="salvar()", :itens="lista(x)", v-if="pode()"
				sub := lex(untemplate([]byte(tk.s)), false)
				for k, st := range sub {
					if st.k != 'i' || k > 0 && sub[k-1].s == "." {
						continue
					}
					tg, ok := local[st.s]
					if !ok && nuxt {
						tg, ok = auto[st.s]
					}
					if !ok {
						continue
					}
					kind := "ref"
					if k+1 < len(sub) && sub[k+1].s == "(" {
						kind = "call"
					}
					g.Refs = append(g.Refs, model.Ref{File: rel, Line: tk.line, From: name, ToFile: tg.file, To: tg.name, Kind: kind})
				}
				continue
			}
			if tk.k == 's' {
				if j > 1 && t[j-1].s == "(" {
					fetch := t[j-2].s == "fetch"
					if p, ok := apiRoute(tk.s); ok || fetch && strings.HasPrefix(tk.s, "/") {
						if !ok {
							p = strings.SplitN(tk.s, "?", 2)[0]
						}
						m := ""
						if fetch {
							m = "GET"
							// fetch(url, { method: 'POST' })
							for k, d := j+1, 1; k+2 < len(t) && d > 0; k++ {
								switch t[k].s {
								case "(":
									d++
								case ")":
									d--
								case "method":
									if t[k+1].s == ":" && t[k+2].k == 's' {
										m = strings.ToUpper(t[k+2].s)
									}
								}
							}
						}
						g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: rel, Line: tk.line, From: name, Path: p, Method: m})
					}
					// supabase.from('t').select() / rpc('f') / storage.from('b')
					if (t[j-2].s == "from" || t[j-2].s == "rpc") && j > 2 && t[j-3].s == "." && j+1 < len(t) && t[j+1].s == ")" {
						typ, recv := "table", ""
						if j > 3 {
							recv = t[j-4].s
						}
						if t[j-2].s == "rpc" {
							typ = "rpc"
						} else if recv == "storage" {
							typ = "bucket"
						}
						var ops []string
						for k := j + 2; k < len(t) && k < j+30; k++ {
							if t[k].k == 'i' && t[k-1].s == "." && (tableOps[t[k].s] || typ == "bucket") {
								ops = append(ops, t[k].s)
								break
							}
						}
						g.DBUses = append(g.DBUses, model.DBUse{File: rel, Line: tk.line, From: name, Type: typ, Name: tk.s, Ops: ops, Client: recv})
					}
				}
				continue
			}
			if tk.k != 'i' {
				continue
			}
			ident := tk.s
			isTag := j > 0 && t[j-1].s == "<"
			if isTag && j+2 < len(t) && t[j+1].s == "-" && t[j+1].line == tk.line {
				// <meu-botao> é o componente MeuBotao
				k := j
				parts := []string{tk.s}
				for k+2 < len(t) && t[k+1].s == "-" && t[k+2].k == 'i' && t[k+2].line == tk.line {
					parts = append(parts, t[k+2].s)
					k += 2
				}
				ident = camel(strings.Join(parts, "-"))
				j = k
			}
			tg, ok := local[ident]
			if !ok && (isTag || nuxt) && strings.HasSuffix(rel, ".vue") {
				tg, ok = auto[ident]
				if ok && tg.file == rel {
					ok = false
				}
			}
			if !ok || tk == t[j] && j > 0 && t[j-1].s == "." {
				continue
			}
			if importLine[tk.line] {
				continue
			}
			kind := "ref"
			switch {
			case isTag:
				kind = "jsx"
			case j+1 < len(t) && t[j+1].s == "(":
				kind = "call"
			}
			g.Refs = append(g.Refs, model.Ref{File: rel, Line: tk.line, From: name, ToFile: tg.file, To: tg.name, Kind: kind})
		}
	}
	if nuxt {
		nuxtServer(root, g)
	} else {
		vueRouter(root, g, resolve, comp)
	}
	return nil
}

// vueRouter lê as rotas do Vue Router ({ path: '/x', component: Tela,
// children: [...] }, inclusive component: () => import('./Tela.vue')).
func vueRouter(root string, g *model.Graph, resolve func(from, spec string) string, comp func(string) string) {
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || sfcSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(p); ext != ".ts" && ext != ".js" && ext != ".mjs" {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil || !(strings.Contains(string(src), "createRouter") || strings.Contains(string(src), "RouteRecordRaw")) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		local := map[string]string{}
		for _, m := range importRe.FindAllSubmatch(src, -1) {
			if tf := resolve(rel, string(m[2])); strings.HasSuffix(tf, ".vue") {
				local[strings.TrimSpace(string(m[1]))] = tf
			}
		}
		type frame struct {
			path, comp string
			line       int
		}
		var stack []frame
		t := lex(untemplate(src), false)
		for i := 0; i < len(t); i++ {
			switch {
			case t[i].s == "{" && t[i].k == 'p':
				stack = append(stack, frame{line: t[i].line})
			case t[i].s == "}" && t[i].k == 'p' && len(stack) > 0:
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if f.path == "" || f.comp == "" {
					continue
				}
				path := f.path
				for k := len(stack) - 1; k >= 0 && !strings.HasPrefix(path, "/"); k-- {
					if stack[k].path != "" {
						path = strings.TrimRight(stack[k].path, "/") + "/" + path
					}
				}
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				g.Pages = append(g.Pages, model.Page{File: f.comp, Path: path, Line: 1, Component: comp(f.comp)})
			case len(stack) > 0 && t[i].k == 'i' && i+2 < len(t) && t[i+1].s == ":" && (i == 0 || t[i-1].s != "."):
				top := &stack[len(stack)-1]
				switch t[i].s {
				case "path":
					if t[i+2].k == 's' {
						top.path, top.line = t[i+2].s, t[i+2].line
					}
				case "component":
					if t[i+2].k == 'i' && local[t[i+2].s] != "" {
						top.comp = local[t[i+2].s]
					}
					// () => import('./Tela.vue')
					for k := i + 2; k < len(t) && k < i+12; k++ {
						if t[k].s == "import" && k+2 < len(t) && t[k+1].s == "(" && t[k+2].k == 's' {
							if tf := resolve(rel, t[k+2].s); tf != "" {
								top.comp = tf
							}
							break
						}
					}
				}
			}
		}
		return nil
	})
}

// nuxtServer: server/api/pedidos/[id].get.ts vira GET /api/pedidos/[id].
func nuxtServer(root string, g *model.Graph) {
	for _, base := range []string{"server/api", "server/routes"} {
		dir := filepath.Join(root, base)
		if !isDir(dir) {
			continue
		}
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			ext := filepath.Ext(p)
			if ext != ".ts" && ext != ".js" && ext != ".mjs" {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			inside, _ := filepath.Rel(dir, p)
			inside = strings.TrimSuffix(filepath.ToSlash(inside), ext)
			method := "ANY"
			if k := strings.LastIndexByte(inside, '.'); k >= 0 {
				if m := httpVerb[inside[k+1:]]; m != "" {
					method = m
					inside = inside[:k]
				}
			}
			inside = strings.TrimSuffix(strings.TrimSuffix(inside, "index"), "/")
			path := "/" + inside
			if base == "server/api" {
				path = strings.TrimSuffix("/api/"+inside, "/")
			}
			g.Routes = append(g.Routes, model.Route{File: rel, Method: method, Path: path, Line: 1, HandlerFile: rel, Handler: "default"})
			return nil
		})
	}
}

func isNuxt(root string) bool {
	for _, n := range []string{"nuxt.config.ts", "nuxt.config.js", "nuxt.config.mjs"} {
		if exists(filepath.Join(root, n)) {
			return true
		}
	}
	return false
}

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
