package extract

// Scala em cima do leitor de Kotlin/Java: def vira fun, trait vira
// interface, os imports do Scala (a.b._, a.b.{X, Y => Z}) viram imports
// simples, e def sem parênteses ou com [T] ganha a forma que o leitor espera.
// As rotas do Play (conf/routes) viram rotas.

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

func scalaToks(t []tok) []tok {
	out := make([]tok, 0, len(t))
	params, depth := false, 0 // dentro dos parâmetros de class X(a: A): viram val
	for i := 0; i < len(t); i++ {
		tk := t[i]
		if params {
			switch {
			case tk.s == "(":
				depth++
			case tk.s == ")":
				depth--
			case depth == 0 && (tk.s == "{" || tk.s == "extends" || tk.s == "with" || tk.s == ";"):
				params = false
			case depth == 1 && tk.k == 'i' && i+1 < len(t) && t[i+1].s == ":" && i > 0 && (t[i-1].s == "(" || t[i-1].s == ","):
				out = append(out, tok{'i', "val", tk.line})
			}
		}
		if tk.k == 'i' && tk.s == "class" && i+1 < len(t) && t[i+1].k == 'i' {
			params, depth = true, 0
		}
		if tk.k != 'i' {
			out = append(out, tk)
			continue
		}
		switch tk.s {
		case "trait":
			tk.s = "interface"
			out = append(out, tk)
		case "def":
			tk.s = "fun"
			out = append(out, tk)
			if i+1 < len(t) && t[i+1].k == 'i' {
				out = append(out, t[i+1])
				i++
				if i+1 < len(t) && t[i+1].s == "[" { // def f[T](…)
					d := 0
					for i+1 < len(t) {
						i++
						if t[i].s == "[" {
							d++
						} else if t[i].s == "]" {
							d--
							if d == 0 {
								break
							}
						}
					}
				}
				if i+1 >= len(t) || t[i+1].s != "(" { // def nome: Int = …
					out = append(out, tok{'p', "(", tk.line}, tok{'p', ")", tk.line})
				}
			}
		case "import":
			// import a.b.{X, Y => Z} / import a.b._
			j := i + 1
			var base []string
			for j < len(t) && t[j].k == 'i' {
				base = append(base, t[j].s)
				j++
				if j+1 < len(t) && t[j].s == "." && (t[j+1].k == 'i' || t[j+1].s == "{" || t[j+1].s == "*") {
					j++
					continue
				}
				break
			}
			line := tk.line
			emit := func(path []string, alias string) {
				out = append(out, tok{'i', "import", line})
				for k, p := range path {
					if k > 0 {
						out = append(out, tok{'p', ".", line})
					}
					if p == "_" {
						out = append(out, tok{'p', "*", line})
						continue
					}
					out = append(out, tok{'i', p, line})
				}
				if alias != "" {
					out = append(out, tok{'i', "as", line}, tok{'i', alias, line})
				}
				out = append(out, tok{'p', ";", line})
			}
			if j < len(t) && t[j].s == "{" {
				for j++; j < len(t) && t[j].s != "}"; j++ {
					if t[j].k != 'i' && t[j].s != "_" {
						continue
					}
					name, alias := t[j].s, ""
					if j+2 < len(t) && (t[j+1].s == "=>" || t[j+1].s == "as") && t[j+2].k == 'i' {
						alias = t[j+2].s
						j += 2
					}
					emit(append(append([]string{}, base...), name), alias)
				}
				i = j
			} else if j < len(t) && t[j].s == "*" {
				emit(append(base, "_"), "")
				i = j
			} else {
				emit(base, "")
				i = j - 1
			}
		default:
			out = append(out, tk)
		}
	}
	return out
}

// playRoutes lê conf/routes: GET /pedidos/:id controllers.Pedidos.mostrar(id: Long)
func playRoutes(root string, g *model.Graph) {
	p := filepath.Join(root, "conf", "routes")
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	byName := map[string]model.Decl{}
	for _, d := range g.Decls {
		if _, dup := byName[d.Name]; !dup {
			byName[d.Name] = d
		}
	}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		fs := strings.Fields(sc.Text())
		if len(fs) < 3 || strings.HasPrefix(fs[0], "#") || httpVerb[strings.ToLower(fs[0])] == "" {
			continue
		}
		call := fs[2]
		if k := strings.IndexByte(call, '('); k >= 0 {
			call = call[:k]
		}
		parts := strings.Split(strings.TrimPrefix(call, "@"), ".")
		h := call
		if len(parts) >= 2 {
			h = parts[len(parts)-2] + "." + parts[len(parts)-1]
		}
		hf := ""
		if d, ok := byName[h]; ok {
			hf = d.File
		}
		g.Routes = append(g.Routes, model.Route{File: "conf/routes", Method: strings.ToUpper(fs[0]), Path: fs[1], Line: n, HandlerFile: hf, Handler: h})
	}
}
