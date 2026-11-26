package extract

// ORMs, lidos por cima do grafo dos outros extratores: Prisma
// (schema.prisma + prisma.pedido.findMany), Drizzle (pgTable("pedidos") +
// db.select().from(pedidos)), Django (models.Model + Pedido.objects.filter)
// e SQLAlchemy (__tablename__ + session.query(Pedido)). Cada uso vira uso de
// tabela com a operação, atribuído à declaração em volta.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

var ormSkip = map[string]bool{"node_modules": true, "dist": true, "build": true, "out": true, ".next": true, ".nuxt": true, ".output": true, "venv": true, "env": true, "__pycache__": true, "site-packages": true, "vendor": true, "coverage": true}

var (
	reDrizzle   = regexp.MustCompile(`(?m)(?:export\s+)?const\s+(\w+)\s*=\s*(?:\w+\.)?(?:pgTable|mysqlTable|sqliteTable|singlestoreTable|table)\s*\(\s*['"]([^'"]+)['"]`)
	rePyClass   = regexp.MustCompile(`(?m)^class\s+(\w+)\s*\(([^)]*)\)\s*:`)
	reDbTable   = regexp.MustCompile(`db_table\s*=\s*['"]([^'"]+)['"]`)
	reTableName = regexp.MustCompile(`__tablename__\s*=\s*['"]([^'"]+)['"]`)
	reAbstract  = regexp.MustCompile(`abstract\s*=\s*True`)
)

var prismaOps = map[string]string{"findMany": "select", "findFirst": "select", "findUnique": "select", "findFirstOrThrow": "select", "findUniqueOrThrow": "select", "count": "select", "aggregate": "select", "groupBy": "select",
	"create": "insert", "createMany": "insert", "createManyAndReturn": "insert", "update": "update", "updateMany": "update", "updateManyAndReturn": "update", "upsert": "upsert", "delete": "delete", "deleteMany": "delete"}

var djangoOps = map[string]string{"create": "insert", "bulk_create": "insert", "get_or_create": "insert", "update": "update", "bulk_update": "update", "update_or_create": "upsert", "delete": "delete"}

var drizzleOps = map[string]string{"from": "select", "leftJoin": "select", "innerJoin": "select", "rightJoin": "select", "fullJoin": "select", "insert": "insert", "update": "update", "delete": "delete"}

// ORM acrescenta ao grafo os usos de tabela feitos por ORMs. Com only, só
// nesses arquivos (o esquema é sempre lido inteiro).
func ORM(root string, g *model.Graph, only []string) {
	var tsFiles, pyFiles, prisma []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || ormSkip[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts":
			tsFiles = append(tsFiles, p)
		case ".py":
			pyFiles = append(pyFiles, p)
		case ".prisma":
			prisma = append(prisma, p)
		}
		return nil
	})
	rel := func(p string) string { r, _ := filepath.Rel(root, p); return filepath.ToSlash(r) }
	want := map[string]bool{}
	for _, o := range only {
		want[o] = true
	}
	wanted := func(r string) bool { return only == nil || want[r] }

	// declarações por arquivo, para saber de onde vem cada uso
	byFile := map[string][]model.Decl{}
	for _, d := range g.Decls {
		byFile[d.File] = append(byFile[d.File], d)
	}
	for _, ds := range byFile {
		sort.Slice(ds, func(i, j int) bool { return ds[i].Line < ds[j].Line })
	}
	from := func(file string, line int) string {
		best := "<module>"
		for _, d := range byFile[file] {
			if d.Line > line {
				break
			}
			best = d.Name
		}
		return best
	}
	use := func(file string, line int, table, op, client string) {
		g.DBUses = append(g.DBUses, model.DBUse{File: file, Line: line, From: from(file, line), Type: "table", Name: table, Ops: []string{op}, Client: client})
	}

	// Prisma: model Pedido { … @@map("pedidos") }
	models := map[string]string{} // propriedade do cliente (pedido) → tabela
	for _, p := range prisma {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		cur := ""
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "model ") {
				f := strings.Fields(l)
				if len(f) >= 2 {
					cur = f[1]
					models[strings.ToLower(cur[:1])+cur[1:]] = cur
				}
				continue
			}
			if cur != "" && strings.HasPrefix(l, "@@map(") {
				if m := regexp.MustCompile(`"([^"]+)"`).FindStringSubmatch(l); m != nil {
					models[strings.ToLower(cur[:1])+cur[1:]] = m[1]
				}
			}
			if l == "}" {
				cur = ""
			}
		}
	}
	// Drizzle: const pedidos = pgTable("pedidos", …)
	drizzle := map[string]string{}
	for _, p := range tsFiles {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "Table(") {
			continue
		}
		for _, m := range reDrizzle.FindAllSubmatch(b, -1) {
			drizzle[string(m[1])] = string(m[2])
		}
	}
	if len(models) > 0 || len(drizzle) > 0 {
		for _, p := range tsFiles {
			r := rel(p)
			if !wanted(r) {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			t := lex(b, false)
			for i := 2; i < len(t); i++ {
				tk := t[i]
				if tk.k != 'i' || t[i-1].s != "." {
					continue
				}
				// prisma.pedido.findMany(…)
				if tb := models[t[i-2].s]; tb != "" && i > 3 && t[i-3].s == "." && i+1 < len(t) && t[i+1].s == "(" {
					if op := prismaOps[tk.s]; op != "" {
						use(r, tk.line, tb, op, "Prisma")
						continue
					}
				}
				// db.select().from(pedidos) / db.insert(pedidos) / db.query.pedidos.findMany()
				if op := drizzleOps[tk.s]; op != "" && i+2 < len(t) && t[i+1].s == "(" && t[i+2].k == 'i' {
					if tb := drizzle[t[i+2].s]; tb != "" && (i+3 >= len(t) || t[i+3].s == ")" || t[i+3].s == ",") {
						use(r, tk.line, tb, op, "Drizzle")
						continue
					}
				}
				if tb := drizzle[tk.s]; tb != "" && t[i-2].s == "query" && i+2 < len(t) && t[i+1].s == "." {
					use(r, tk.line, tb, "select", "Drizzle")
				}
			}
		}
	}

	// Django e SQLAlchemy
	django, alchemy := map[string]string{}, map[string]string{}
	pySrc := map[string][]byte{}
	for _, p := range pyFiles {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := string(b)
		isModels := strings.HasSuffix(p, "/models.py") || strings.Contains(p, "/models/")
		hasTable := strings.Contains(s, "__tablename__")
		if !isModels && !hasTable && !strings.Contains(s, ".objects.") && !strings.Contains(s, "query") && !strings.Contains(s, "select(") {
			continue
		}
		pySrc[p] = b
		locs := rePyClass.FindAllSubmatchIndex(b, -1)
		for k, m := range locs {
			name, bases := s[m[2]:m[3]], s[m[4]:m[5]]
			end := len(s)
			if k+1 < len(locs) {
				end = locs[k+1][0]
			}
			body := s[m[1]:end]
			if hasTable {
				if tm := reTableName.FindStringSubmatch(body); tm != nil {
					alchemy[name] = tm[1]
				}
			}
			if isModels && (strings.Contains(bases, "Model") || django[strings.TrimSpace(bases)] != "") && !reAbstract.MatchString(body) {
				tb := ""
				if tm := reDbTable.FindStringSubmatch(body); tm != nil {
					tb = tm[1]
				} else {
					dir := filepath.Dir(p)
					if strings.Contains(p, "/models/") {
						dir = p[:strings.LastIndex(p, "/models/")]
					}
					tb = filepath.Base(dir) + "_" + strings.ToLower(name)
				}
				django[name] = tb
			}
		}
	}
	if len(django) == 0 && len(alchemy) == 0 {
		return
	}
	for p, b := range pySrc {
		r := rel(p)
		if !wanted(r) {
			continue
		}
		t := lexHash(b, hashPython)
		for i := 0; i < len(t); i++ {
			tk := t[i]
			if tk.k != 'i' || i > 0 && t[i-1].s == "." {
				continue
			}
			// Pedido.objects.filter(…)
			if tb := django[tk.s]; tb != "" && i+4 < len(t) && t[i+1].s == "." && t[i+2].s == "objects" && t[i+3].s == "." && t[i+4].k == 'i' {
				op := djangoOps[t[i+4].s]
				if op == "" {
					op = "select"
				}
				use(r, tk.line, tb, chainOpPy(t, i+5, op), "Django")
				continue
			}
			if tb := alchemy[tk.s]; tb != "" {
				// session.query(Pedido) / select(Pedido) / insert(Pedido) / Pedido.query.filter
				if i > 1 && t[i-1].s == "(" {
					switch t[i-2].s {
					case "query", "select", "get":
						use(r, tk.line, tb, "select", "SQLAlchemy")
					case "insert":
						use(r, tk.line, tb, "insert", "SQLAlchemy")
					case "update":
						use(r, tk.line, tb, "update", "SQLAlchemy")
					case "delete":
						use(r, tk.line, tb, "delete", "SQLAlchemy")
					}
				} else if i+2 < len(t) && t[i+1].s == "." && t[i+2].s == "query" {
					use(r, tk.line, tb, "select", "SQLAlchemy")
				}
			}
		}
	}
}

// chainOpPy: Pedido.objects.filter(…).update(…) é escrita.
func chainOpPy(t []tok, j int, op string) string {
	for j < len(t) {
		if t[j].s == "(" {
			d := 0
			for ; j < len(t); j++ {
				if t[j].s == "(" {
					d++
				} else if t[j].s == ")" {
					d--
					if d == 0 {
						break
					}
				}
			}
			j++
			continue
		}
		if t[j].s == "." && j+1 < len(t) && t[j+1].k == 'i' {
			if o := djangoOps[t[j+1].s]; o != "" {
				op = o
			}
			j += 2
			continue
		}
		break
	}
	return op
}
