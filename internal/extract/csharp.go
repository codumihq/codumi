package extract

// C# em cima do leitor de Java: os atributos [HttpGet("x")] viram anotações,
// o Entity Framework vira uso de tabela (DbSet<T> Nome, com [Table("x")] na
// entidade) e as minimal APIs (app.MapGet("/x", Handler)) viram rotas.

import (
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

// csAttributes troca [Nome(args)] por um token de anotação seguido dos argumentos.
func csAttributes(t []tok) []tok {
	out := make([]tok, 0, len(t))
	for i := 0; i < len(t); i++ {
		tk := t[i]
		prevOK := i == 0 || t[i-1].k == 'p' && strings.Contains(";{}](,", t[i-1].s)
		if !(tk.k == 'p' && tk.s == "[" && prevOK && i+1 < len(t) && t[i+1].k == 'i') {
			out = append(out, tk)
			continue
		}
		// acha o ] que fecha
		end, d := -1, 0
		for j := i; j < len(t); j++ {
			if t[j].s == "[" {
				d++
			} else if t[j].s == "]" {
				d--
				if d == 0 {
					end = j
					break
				}
			} else if t[j].s == ";" || t[j].s == "{" {
				break
			}
		}
		if end < 0 {
			out = append(out, tk)
			continue
		}
		j := i + 1
		for j < end {
			// alvo opcional: [return: X], [assembly: X]
			if j+1 < end && t[j+1].s == ":" {
				j += 2
				continue
			}
			name := ""
			for j < end && (t[j].k == 'i' || t[j].s == ".") {
				if t[j].k == 'i' {
					name = t[j].s
				}
				j++
			}
			if name != "" {
				out = append(out, tok{'a', strings.TrimSuffix(name, "Attribute"), t[i+1].line})
			}
			if j < end && t[j].s == "(" {
				pd := 0
				for ; j < end; j++ {
					out = append(out, t[j])
					if t[j].s == "(" {
						pd++
					} else if t[j].s == ")" {
						pd--
						if pd == 0 {
							j++
							break
						}
					}
				}
			}
			if j < end && t[j].s == "," {
				j++
			}
			if name == "" && j < end {
				j++
			}
		}
		i = end
	}
	return out
}

// efSets acha os DbSet<Entidade> Nome de todos os arquivos C# e devolve
// Nome → tabela ([Table("x")] na entidade, senão o próprio nome do DbSet).
func efSets(files []*jvmFile) map[string]string {
	tables := map[string]string{} // entidade → [Table]
	sets := map[string]string{}
	for _, f := range files {
		if !f.cs {
			continue
		}
		t := f.toks
		for i := 0; i+4 < len(t); i++ {
			if t[i].k == 'a' && t[i].s == "Table" && i+2 < len(t) && t[i+1].s == "(" && t[i+2].k == 's' {
				for j := i + 3; j < len(t) && j < i+20; j++ {
					if t[j].s == "class" && j+1 < len(t) {
						tables[t[j+1].s] = t[i+2].s
						break
					}
				}
			}
		}
	}
	for _, f := range files {
		if !f.cs {
			continue
		}
		t := f.toks
		for i := 0; i+4 < len(t); i++ {
			if t[i].s == "DbSet" && t[i+1].s == "<" && t[i+2].k == 'i' && t[i+3].s == ">" && t[i+4].k == 'i' {
				name := t[i+4].s
				if tb := tables[t[i+2].s]; tb != "" {
					sets[name] = tb
				} else {
					sets[name] = name
				}
			}
		}
	}
	return sets
}

var efOps = map[string]string{"Add": "insert", "AddAsync": "insert", "AddRange": "insert", "AddRangeAsync": "insert",
	"Remove": "delete", "RemoveRange": "delete", "ExecuteDelete": "delete", "ExecuteDeleteAsync": "delete",
	"Update": "update", "UpdateRange": "update", "ExecuteUpdate": "update", "ExecuteUpdateAsync": "update", "Attach": "update"}

var minimalAPI = map[string]string{"MapGet": "GET", "MapPost": "POST", "MapPut": "PUT", "MapDelete": "DELETE", "MapPatch": "PATCH"}

// csExtras trata, no token i, o que é só do C#: minimal APIs e DbSet.
func (f *jvmFile) csExtras(t []tok, i int, from, prefix string, g *model.Graph, sets map[string]string, lookup func(string) (*jvmDecl, string)) {
	tk := t[i]
	// app.MapGet("/x", Handler) / app.MapPost("/x", async (...) => …)
	if m, ok := minimalAPI[tk.s]; ok && i > 0 && t[i-1].s == "." && i+2 < len(t) && t[i+1].s == "(" && t[i+2].k == 's' {
		h, hf := from, f.rel
		if i+4 < len(t) && t[i+3].s == "," && t[i+4].k == 'i' && (i+5 >= len(t) || t[i+5].s == ")" || t[i+5].s == ".") {
			name := t[i+4].s
			if i+6 < len(t) && t[i+5].s == "." && t[i+6].k == 'i' {
				name = t[i+4].s + "." + t[i+6].s
			}
			h = name
			if d, _ := lookup(strings.SplitN(name, ".", 2)[0]); d != nil {
				hf = d.file
			}
		}
		// .RequireAuthorization() / .AllowAnonymous() no fim do comando
		gs := "none"
		if f.globalAuth || f.groupAuth {
			gs = ""
		}
		d := 0
		for j := i + 1; j < len(t); j++ {
			if t[j].s == "(" || t[j].s == "{" {
				d++
			} else if t[j].s == ")" || t[j].s == "}" {
				d--
			}
			if d <= 0 && t[j].s == ";" {
				break
			}
			if t[j].s == "RequireAuthorization" {
				gs = "Authorize"
			} else if t[j].s == "AllowAnonymous" {
				gs = "none"
			}
		}
		g.Routes = append(g.Routes, model.Route{File: f.rel, Method: m, Path: joinPath(prefix, t[i+2].s), Line: tk.line, HandlerFile: hf, Handler: h, Guards: gs})
		return
	}
	// _db.Pedidos.Where(…) / context.Pedidos.Add(…)
	if table, ok := sets[tk.s]; ok && i > 1 && t[i-1].s == "." && i+2 < len(t) && t[i+1].s == "." && t[i+2].k == 'i' {
		op := efOps[t[i+2].s]
		if op == "" {
			op = "select"
		}
		g.DBUses = append(g.DBUses, model.DBUse{File: f.rel, Line: tk.line, From: from, Type: "table", Name: table, Ops: []string{op}, Client: t[i-2].s})
	}
}
