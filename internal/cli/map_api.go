package cli

// API do mapa para o app: procurar declarações e ver, em volta de uma delas,
// quem chama, o que ela chama, as rotas e telas que chegam nela e o que ela
// toca no banco (com o estado de RLS de cada tabela).

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/store"
)

type MapItem struct {
	File  string `json:"file"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Line  int    `json:"line"`
	Count int    `json:"count,omitempty"` // usos (na busca) ou ocorrências da ligação
	Lines []int  `json:"lines,omitempty"` // onde a ligação acontece
	Route string `json:"route,omitempty"` // rota ou tela que essa declaração trata
}

type MapDB struct {
	Type   string   `json:"type"`
	Name   string   `json:"name"`
	Ops    []string `json:"ops"`
	Line   int      `json:"line"`
	RLS    *bool    `json:"rls,omitempty"`
	Admin  bool     `json:"admin,omitempty"`  // cliente que passa por cima do RLS
	SecDef bool     `json:"secdef,omitempty"` // função SECURITY DEFINER
}

type MapRoute struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Kind   string `json:"kind"` // route (trata), page (tela), call (código chama a rota)
}

type MapNode struct {
	Node    MapItem    `json:"node"`
	Callers []MapItem  `json:"callers"`
	Callees []MapItem  `json:"callees"`
	Routes  []MapRoute `json:"routes"`
	DB      []MapDB    `json:"db"`
}

func openMap(root string) (*sql.DB, error) {
	return store.Open(filepath.Join(root, store.Dir, store.File))
}

// MapSearch procura declarações pelo nome, as mais usadas primeiro.
func MapSearch(root, q string, limit int) ([]MapItem, error) {
	db, err := openMap(root)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if limit <= 0 {
		limit = 30
	}
	q = strings.TrimSpace(q)
	rows, err := db.Query(`SELECT d.file, d.name, d.kind, d.line,
		(SELECT COUNT(*) FROM refs r WHERE r.to_file = d.file AND r.to_name = d.name AND r.kind NOT IN ('import','export')) AS n
		FROM decls d WHERE d.name LIKE ? ORDER BY (d.name = ?) DESC, (d.name LIKE ?) DESC, n DESC, d.name LIMIT ?`,
		"%"+q+"%", q, q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MapItem{}
	for rows.Next() {
		var it MapItem
		rows.Scan(&it.File, &it.Name, &it.Kind, &it.Line, &it.Count)
		out = append(out, it)
	}
	return out, nil
}

// MapAround devolve a vizinhança de uma declaração.
func MapAround(root, file, name string) (MapNode, error) {
	var m MapNode
	db, err := openMap(root)
	if err != nil {
		return m, err
	}
	defer db.Close()
	m.Node = MapItem{File: file, Name: name}
	db.QueryRow(`SELECT kind, line FROM decls WHERE file = ? AND name = ?`, file, name).Scan(&m.Node.Kind, &m.Node.Line)
	kindOf := func(f, n string) (string, int) {
		var k string
		var l int
		db.QueryRow(`SELECT kind, line FROM decls WHERE file = ? AND name = ?`, f, n).Scan(&k, &l)
		return k, l
	}
	group := func(q string, args ...any) []MapItem {
		rows, err := db.Query(q, args...)
		if err != nil {
			return nil
		}
		defer rows.Close()
		idx := map[string]*MapItem{}
		var order []string
		for rows.Next() {
			var f, n string
			var line int
			rows.Scan(&f, &n, &line)
			if n == "" {
				n = "<module>"
			}
			k := f + "|" + n
			if idx[k] == nil {
				it := &MapItem{File: f, Name: n}
				it.Kind, it.Line = kindOf(f, n)
				idx[k] = it
				order = append(order, k)
			}
			idx[k].Count++
			if len(idx[k].Lines) < 8 {
				idx[k].Lines = append(idx[k].Lines, line)
			}
		}
		out := []MapItem{}
		for _, k := range order {
			out = append(out, *idx[k])
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
		return out
	}
	// quem chama: as declarações em que estão os usos
	m.Callers = group(`SELECT file, from_name, line FROM refs WHERE to_file = ? AND to_name = ? AND kind NOT IN ('import','export') ORDER BY file, line`, file, name)
	// o que ela chama: usos feitos de dentro dela (membros de uma classe contam para a classe)
	m.Callees = group(`SELECT to_file, to_name, line FROM refs WHERE file = ? AND (from_name = ? OR from_name LIKE ?) AND kind NOT IN ('import','export') AND NOT (to_file = ? AND to_name = ?) ORDER BY line`, file, name, name+".%", file, name)
	// rotas e telas que ela trata
	if rows, err := db.Query(`SELECT method, path FROM routes WHERE handler_file = ? AND handler = ?`, file, name); err == nil {
		for rows.Next() {
			var r MapRoute
			rows.Scan(&r.Method, &r.Path)
			r.Kind = "route"
			m.Routes = append(m.Routes, r)
		}
		rows.Close()
	}
	if rows, err := db.Query(`SELECT path FROM pages WHERE file = ? AND component = ?`, file, name); err == nil {
		for rows.Next() {
			var r MapRoute
			rows.Scan(&r.Path)
			r.Kind = "page"
			m.Routes = append(m.Routes, r)
		}
		rows.Close()
	}
	if rows, err := db.Query(`SELECT DISTINCT method, path FROM route_calls WHERE file = ? AND (from_name = ? OR from_name LIKE ?)`, file, name, name+".%"); err == nil {
		for rows.Next() {
			var r MapRoute
			rows.Scan(&r.Method, &r.Path)
			r.Kind = "call"
			m.Routes = append(m.Routes, r)
		}
		rows.Close()
	}
	// banco: o que ela toca, com RLS da tabela e SECURITY DEFINER da função
	if rows, err := db.Query(`SELECT type, name, ops, line, client FROM db_uses WHERE file = ? AND (from_name = ? OR from_name LIKE ?) ORDER BY line`, file, name, name+".%"); err == nil {
		seen := map[string]int{}
		for rows.Next() {
			var d MapDB
			var ops, client string
			rows.Scan(&d.Type, &d.Name, &ops, &d.Line, &client)
			d.Admin = bypassesRLS(client)
			k := d.Type + "|" + d.Name
			if i, ok := seen[k]; ok {
				for _, o := range strings.Split(ops, ",") {
					if o = strings.TrimSpace(o); o != "" && !contains(m.DB[i].Ops, o) {
						m.DB[i].Ops = append(m.DB[i].Ops, o)
					}
				}
				m.DB[i].Admin = m.DB[i].Admin || d.Admin
				continue
			}
			for _, o := range strings.Split(ops, ",") {
				if o = strings.TrimSpace(o); o != "" {
					d.Ops = append(d.Ops, o)
				}
			}
			seen[k] = len(m.DB)
			m.DB = append(m.DB, d)
		}
		rows.Close()
	}
	for i := range m.DB {
		d := &m.DB[i]
		switch d.Type {
		case "table":
			var rls int
			if db.QueryRow(`SELECT rls FROM sql_tables WHERE name = ? ORDER BY is_view LIMIT 1`, d.Name).Scan(&rls) == nil {
				v := rls == 1
				d.RLS = &v
			}
		case "rpc":
			var sd int
			if db.QueryRow(`SELECT secdef FROM sql_functions WHERE name = ? LIMIT 1`, d.Name).Scan(&sd) == nil {
				d.SecDef = sd == 1
			}
		}
	}
	// quem é handler de rota ou tela ganha o caminho, que diz mais que "POST"
	label := func(it *MapItem) {
		var meth, path string
		if db.QueryRow(`SELECT method, path FROM routes WHERE handler_file = ? AND handler = ? LIMIT 1`, it.File, it.Name).Scan(&meth, &path) == nil {
			it.Route = strings.TrimSpace(meth + " " + path)
		} else if db.QueryRow(`SELECT path FROM pages WHERE file = ? AND component = ? LIMIT 1`, it.File, it.Name).Scan(&path) == nil {
			it.Route = path
		}
	}
	label(&m.Node)
	for i := range m.Callers {
		label(&m.Callers[i])
	}
	for i := range m.Callees {
		label(&m.Callees[i])
	}
	if m.Callers == nil {
		m.Callers = []MapItem{}
	}
	if m.Callees == nil {
		m.Callees = []MapItem{}
	}
	if m.Routes == nil {
		m.Routes = []MapRoute{}
	}
	if m.DB == nil {
		m.DB = []MapDB{}
	}
	return m, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
