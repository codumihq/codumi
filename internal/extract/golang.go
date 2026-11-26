package extract

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/codumihq/codumi/internal/model"
)

// GoModules returns the directories under root that hold a go.mod.
func GoModules(root string) []string {
	var mods []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if p != root && (strings.HasPrefix(n, ".") || n == "vendor" || n == "node_modules" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			mods = append(mods, filepath.Dir(p))
		}
		return nil
	})
	sort.Strings(mods)
	return mods
}

// Go extracts every Go module under root using go/packages and the type
// checker, so references are exact like on the TypeScript side.
// If mods is non-nil, only those module directories are loaded.
func Go(root string, g *model.Graph, mods []string) error {
	if mods == nil {
		mods = GoModules(root)
	}
	for _, dir := range mods {
		if err := goModule(root, dir, g); err != nil {
			rel, _ := filepath.Rel(root, dir)
			g.Warnings = append(g.Warnings, fmt.Sprintf("go module %s: %v", rel, err))
		}
	}
	return nil
}

type declRange struct {
	pos, end token.Pos
	name     string
}

var (
	httpVerbs   = map[string]string{"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH", "Delete": "DELETE", "Head": "HEAD", "Options": "OPTIONS", "GET": "GET", "POST": "POST", "PUT": "PUT", "PATCH": "PATCH", "DELETE": "DELETE", "HEAD": "HEAD", "OPTIONS": "OPTIONS"}
	reSQLMethod = regexp.MustCompile(`^(Query|QueryRow|QueryContext|QueryRowContext|Exec|ExecContext|Select|SelectContext|Get|GetContext|NamedExec|NamedExecContext|NamedQuery|Prepare|PrepareContext|CopyFrom)$`)
	reLooksSQL  = regexp.MustCompile(`(?is)^\s*(select|insert|update|delete|with|upsert|merge)\b`)
	reSQLTable  = regexp.MustCompile(`(?i)\b(from|join|into|update)\s+((?:"?[a-z_][a-z0-9_]*"?\.)?"?[a-z_][a-z0-9_]*"?)`)
	reSQLDelete = regexp.MustCompile(`(?i)^\s*(?:with\b.*?\)\s*)?delete\s+from\s+((?:"?[a-z_][a-z0-9_]*"?\.)?"?[a-z_][a-z0-9_]*"?)`)
	reFmtVerb   = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)
	sqlNotTable = map[string]bool{"select": true, "set": true, "only": true, "lateral": true, "unnest": true, "generate_series": true, "jsonb_array_elements": true, "json_array_elements": true}
)

func goModule(root, dir string, g *model.Graph) error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedModule | packages.NeedImports,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return err
	}
	modPath := ""
	for _, p := range pkgs {
		if p.Module != nil {
			modPath = p.Module.Path
			break
		}
	}
	inMod := func(p *types.Package) bool {
		return p != nil && modPath != "" && (p.Path() == modPath || strings.HasPrefix(p.Path(), modPath+"/"))
	}
	var fset *token.FileSet
	broken := 0
	seenFile := map[string]bool{}

	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			broken++
		}
		if pkg.TypesInfo == nil {
			continue
		}
		fset = pkg.Fset
		info := pkg.TypesInfo
		rel := func(pos token.Pos) (string, int) {
			p := fset.Position(pos)
			r, err := filepath.Rel(root, p.Filename)
			if err != nil {
				return p.Filename, p.Line
			}
			return filepath.ToSlash(r), p.Line
		}
		constStr := func(e ast.Expr) (string, bool) {
			if tv, ok := info.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
				return constant.StringVal(tv.Value), true
			}
			return "", false
		}
		// pathOf turns base + "/v1/x/" + id or fmt.Sprintf("%s/v1/x/%s", …)
		// into "/v1/x/${}".
		var pathOf func(e ast.Expr) string
		pathOf = func(e ast.Expr) string {
			if s, ok := constStr(e); ok {
				return s
			}
			switch x := e.(type) {
			case *ast.BinaryExpr:
				if x.Op == token.ADD {
					return pathOf(x.X) + pathOf(x.Y)
				}
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" && len(x.Args) > 0 {
					if s, ok := constStr(x.Args[0]); ok {
						return reFmtVerb.ReplaceAllString(s, "${}")
					}
				}
			case *ast.ParenExpr:
				return pathOf(x.X)
			}
			return "${}"
		}

		for _, file := range pkg.Syntax {
			fname := fset.Position(file.Pos()).Filename
			if !strings.HasSuffix(fname, ".go") || strings.Contains(fname, "/vendor/") {
				continue
			}
			f, _ := rel(file.Pos())
			if seenFile[f] {
				continue
			}
			seenFile[f] = true
			g.Files = append(g.Files, model.File{Path: f, Lines: fset.File(file.Pos()).LineCount()})

			var ranges []declRange
			for _, d := range file.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					name, kind := d.Name.Name, "function"
					if d.Recv != nil && len(d.Recv.List) > 0 {
						name, kind = recvName(d.Recv.List[0].Type)+"."+name, "method"
					}
					_, line := rel(d.Pos())
					g.Decls = append(g.Decls, model.Decl{File: f, Name: name, Kind: kind, Line: line, Exported: d.Name.IsExported()})
					ranges = append(ranges, declRange{d.Pos(), d.End(), name})
				case *ast.GenDecl:
					for _, sp := range d.Specs {
						switch s := sp.(type) {
						case *ast.TypeSpec:
							_, line := rel(s.Pos())
							g.Decls = append(g.Decls, model.Decl{File: f, Name: s.Name.Name, Kind: "type", Line: line, Exported: s.Name.IsExported()})
							ranges = append(ranges, declRange{s.Pos(), s.End(), s.Name.Name})
						case *ast.ValueSpec:
							kind := "var"
							if d.Tok == token.CONST {
								kind = "const"
							}
							for _, n := range s.Names {
								if n.Name == "_" {
									continue
								}
								_, line := rel(n.Pos())
								g.Decls = append(g.Decls, model.Decl{File: f, Name: n.Name, Kind: kind, Line: line, Exported: n.IsExported()})
							}
							if len(s.Names) > 0 {
								ranges = append(ranges, declRange{s.Pos(), s.End(), s.Names[0].Name})
							}
						}
					}
				}
			}
			anchor := func(pos token.Pos) string {
				for _, r := range ranges {
					if pos >= r.pos && pos < r.end {
						return r.name
					}
				}
				return "<package>"
			}
			funcRef := func(e ast.Expr) (string, string, bool) {
				var id *ast.Ident
				switch x := e.(type) {
				case *ast.Ident:
					id = x
				case *ast.SelectorExpr:
					id = x.Sel
				default:
					return "", "", false
				}
				obj := info.Uses[id]
				fn, ok := obj.(*types.Func)
				if !ok || !inMod(fn.Pkg()) {
					return "", "", false
				}
				name, ok := goTarget(fn)
				if !ok {
					return "", "", false
				}
				tf, _ := rel(fn.Pos())
				return tf, name, true
			}
			// handlerOf finds the handler inside s.admin(s.handler) or
			// http.HandlerFunc(h): the innermost function passed as a value.
			var handlerOf func(e ast.Expr) (string, string)
			handlerOf = func(e ast.Expr) (string, string) {
				switch x := e.(type) {
				case *ast.CallExpr:
					for j := len(x.Args) - 1; j >= 0; j-- {
						if hf, h := handlerOf(x.Args[j]); h != "" {
							return hf, h
						}
					}
					// s.Connect() style: the call itself builds the handler.
					if hf, h, ok := funcRef(x.Fun); ok {
						return hf, h
					}
				case *ast.FuncLit:
					return f, anchor(x.Pos())
				case *ast.ParenExpr:
					return handlerOf(x.X)
				default:
					if hf, h, ok := funcRef(e); ok {
						return hf, h
					}
				}
				return "", ""
			}

			// guardsOf lists the wrappers around a handler: s.admin(h) → admin,
			// c.Then(h) with c := alice.New(s.auth) is not resolved (only direct calls).
			guardsOf := func(e ast.Expr) string {
				var gs []string
				ast.Inspect(e, func(n ast.Node) bool {
					if c, ok := n.(*ast.CallExpr); ok {
						var name string
						switch fn := c.Fun.(type) {
						case *ast.Ident:
							name = fn.Name
						case *ast.SelectorExpr:
							name = fn.Sel.Name
						}
						if name != "" {
							gs = append(gs, name)
						}
					}
					return true
				})
				return strings.Join(gs, ",")
			}

			var stack []ast.Node
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				switch x := n.(type) {
				case *ast.Ident:
					obj := info.Uses[x]
					if obj == nil {
						return true
					}
					switch obj.(type) {
					case *types.PkgName, *types.Label, *types.Builtin, *types.Nil:
						return true
					}
					var node ast.Node = x
					parentIdx := len(stack) - 2
					if parentIdx >= 0 {
						if sel, ok := stack[parentIdx].(*ast.SelectorExpr); ok && sel.Sel == x {
							node = sel
							parentIdx--
						}
					}
					kind := "ref"
					if parentIdx >= 0 {
						if call, ok := stack[parentIdx].(*ast.CallExpr); ok && call.Fun == node {
							kind = "call"
						}
					}
					if _, ok := obj.(*types.TypeName); ok {
						kind = "type"
					}
					_, line := rel(x.Pos())
					if inMod(obj.Pkg()) {
						name, ok := goTarget(obj)
						if !ok {
							return true
						}
						tf, _ := rel(obj.Pos())
						if tf == f && obj.Pos() == x.Pos() {
							return true
						}
						g.Refs = append(g.Refs, model.Ref{File: f, Line: line, From: anchor(x.Pos()), ToFile: tf, To: name, Kind: kind})
					} else if fn, ok := obj.(*types.Func); ok && kind == "call" && fn.Pkg() != nil {
						// External calls (stdlib, deps) are kept too, as pkg.Name,
						// so checks like hmac.Equal are visible in the graph.
						name, ok := goTarget(fn)
						if !ok {
							name = fn.Name()
						}
						g.Refs = append(g.Refs, model.Ref{File: f, Line: line, From: anchor(x.Pos()), ToFile: "ext:" + fn.Pkg().Path(), To: fn.Pkg().Name() + "." + name, Kind: kind})
					}
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					m := sel.Sel.Name
					_, line := rel(x.Pos())
					// routes
					if (m == "HandleFunc" || m == "Handle") && len(x.Args) >= 2 {
						if pat, ok := constStr(x.Args[0]); ok && strings.Contains(pat, "/") {
							method, path := "ANY", pat
							if sp := strings.IndexByte(pat, ' '); sp > 0 {
								method, path = strings.ToUpper(pat[:sp]), strings.TrimSpace(pat[sp+1:])
							}
							if i := strings.IndexByte(path, '/'); i > 0 { // host-qualified pattern
								path = path[i:]
							}
							// gorilla: .HandleFunc(...).Methods("POST")
							if len(stack) >= 3 {
								if ps, ok := stack[len(stack)-2].(*ast.SelectorExpr); ok && ps.Sel.Name == "Methods" {
									if pc, ok := stack[len(stack)-3].(*ast.CallExpr); ok && len(pc.Args) > 0 {
										if v, ok := constStr(pc.Args[0]); ok {
											method = strings.ToUpper(v)
										}
									}
								}
							}
							hf, h := handlerOf(x.Args[len(x.Args)-1])
							if h == "" {
								hf, h = f, anchor(x.Pos())
							}
							g.Routes = append(g.Routes, model.Route{File: f, Method: method, Path: path, Line: line, HandlerFile: hf, Handler: h, Guards: guardsOf(x.Args[len(x.Args)-1])})
						}
					} else if verb, ok := httpVerbs[m]; ok && len(x.Args) >= 2 {
						if path, ok := constStr(x.Args[0]); ok && strings.HasPrefix(path, "/") {
							hf, h := handlerOf(x.Args[len(x.Args)-1])
							if h == "" {
								hf, h = f, anchor(x.Pos())
							}
							g.Routes = append(g.Routes, model.Route{File: f, Method: verb, Path: path, Line: line, HandlerFile: hf, Handler: h, Guards: guardsOf(x.Args[len(x.Args)-1])})
						}
					}
					// SQL
					if reSQLMethod.MatchString(m) {
						for _, a := range x.Args {
							q, ok := constStr(a)
							if !ok || !reLooksSQL.MatchString(q) {
								continue
							}
							for _, t := range sqlTables(q) {
								g.DBUses = append(g.DBUses, model.DBUse{File: f, Line: line, From: anchor(x.Pos()), Type: "table", Name: t.name, Ops: t.ops, Client: clip(types.ExprString(sel.X), 60)})
							}
							break
						}
					}
					// outbound HTTP calls to API paths
					if (m == "NewRequest" || m == "NewRequestWithContext") && len(x.Args) >= 2 {
						ui, mi := 1, 0
						if m == "NewRequestWithContext" {
							ui, mi = 2, 1
						}
						if ui < len(x.Args) {
							p := strings.TrimPrefix(pathOf(x.Args[ui]), "${}")
							if strings.HasPrefix(p, "/") {
								method, _ := constStr(x.Args[mi])
								if method == "" {
									if s, ok := x.Args[mi].(*ast.SelectorExpr); ok && strings.HasPrefix(s.Sel.Name, "Method") {
										method = strings.ToUpper(strings.TrimPrefix(s.Sel.Name, "Method"))
									}
								}
								g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: f, Line: line, From: anchor(x.Pos()), Path: strings.SplitN(p, "?", 2)[0], Method: method})
							}
						}
					}
				}
				return true
			})
		}
	}
	if broken > 0 {
		g.Warnings = append(g.Warnings, fmt.Sprintf("go: %d package(s) had load or type errors; their results may be partial", broken))
	}
	return nil
}

func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return "?"
}

// goTarget names a package-level object (or Type.Method) like decls do.
func goTarget(obj types.Object) (string, bool) {
	switch o := obj.(type) {
	case *types.Func:
		o = o.Origin()
		sig, _ := o.Type().(*types.Signature)
		if sig != nil && sig.Recv() != nil {
			t := sig.Recv().Type()
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			if n, ok := t.(*types.Named); ok {
				return n.Origin().Obj().Name() + "." + o.Name(), true
			}
			return "", false
		}
		return o.Name(), true
	case *types.TypeName, *types.Const, *types.Var:
		if obj.Pkg() != nil && obj.Parent() == obj.Pkg().Scope() {
			return obj.Name(), true
		}
	}
	return "", false
}

type sqlTable struct {
	name string
	ops  []string
}

func sqlTables(q string) []sqlTable {
	var out []sqlTable
	idx := map[string]int{}
	add := func(name, op string) {
		name = strings.ToLower(strings.ReplaceAll(name, `"`, ""))
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:]
		}
		if sqlNotTable[name] {
			return
		}
		i, ok := idx[name]
		if !ok {
			idx[name] = len(out)
			out = append(out, sqlTable{name: name})
			i = len(out) - 1
		}
		for _, o := range out[i].ops {
			if o == op {
				return
			}
		}
		out[i].ops = append(out[i].ops, op)
	}
	if m := reSQLDelete.FindStringSubmatch(q); m != nil {
		add(m[1], "delete")
	}
	for _, ix := range reSQLTable.FindAllStringSubmatchIndex(q, -1) {
		m := []string{q[ix[0]:ix[1]], q[ix[2]:ix[3]], q[ix[4]:ix[5]]}
		if strings.EqualFold(m[1], "from") && strings.HasSuffix(strings.ToLower(strings.TrimRight(q[:ix[0]], " \t\n")), "delete") {
			continue // DELETE FROM x is a delete, not a select
		}
		switch strings.ToLower(m[1]) {
		case "into":
			add(m[2], "insert")
		case "update":
			add(m[2], "update")
		default:
			add(m[2], "select")
		}
	}
	return out
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
