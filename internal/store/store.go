// Package store writes the project graph to a SQLite file and reads it back.
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/codumihq/codumi/internal/model"
	_ "modernc.org/sqlite"
)

const Dir = ".codumi"
const File = "codumi.db"

// GitIgnore keeps the index out of git but lets module notes in.
// notes/ e team/ (o que o time compartilha: memória, pedidos salvos) vão para o git
const GitIgnore = "*\n!.gitignore\n!notes/\n!notes/**\n!team/\n!team/**\n"

const schema = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE files(path TEXT PRIMARY KEY, lines INT);
CREATE TABLE decls(file TEXT, name TEXT, kind TEXT, line INT, exported INT);
CREATE TABLE refs(file TEXT, line INT, from_name TEXT, to_file TEXT, to_name TEXT, kind TEXT);
CREATE TABLE db_uses(file TEXT, line INT, from_name TEXT, type TEXT, name TEXT, ops TEXT, client TEXT);
CREATE TABLE routes(file TEXT, method TEXT, path TEXT, line INT, handler_file TEXT, handler TEXT, guards TEXT);
CREATE TABLE pages(file TEXT, path TEXT, line INT, component TEXT);
CREATE TABLE route_calls(file TEXT, line INT, from_name TEXT, path TEXT, method TEXT);
CREATE TABLE sql_tables(schema TEXT, name TEXT, is_view INT, invoker INT, rls INT, anon_grant INT, src TEXT);
CREATE TABLE sql_policies(schema TEXT, table_name TEXT, name TEXT, cmd TEXT, roles TEXT, permissive INT, using_expr TEXT, check_expr TEXT, src TEXT);
CREATE TABLE sql_functions(schema TEXT, name TEXT, returns TEXT, secdef INT, caller_scoped INT, exec_roles TEXT, src TEXT, args TEXT);
CREATE TABLE sql_refs(kind TEXT, name TEXT, to_name TEXT, src TEXT);
CREATE INDEX sql_refs_to ON sql_refs(to_name);
CREATE TABLE buckets(id TEXT, public INT, src TEXT);
CREATE TABLE file_state(path TEXT PRIMARY KEY, size INT, mtime INT, lang TEXT);
CREATE INDEX refs_to ON refs(to_name, to_file);
CREATE INDEX refs_from ON refs(file, from_name);
CREATE INDEX decls_name ON decls(name);
CREATE INDEX db_name ON db_uses(name);
CREATE INDEX db_from ON db_uses(file, from_name);
`

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Save replaces the index at root/.codumi/codumi.db with g.
func Save(root string, g *model.Graph, meta map[string]string, states []FileState) (string, error) {
	migrateProject(root)
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Keep the index out of git without touching the user's .gitignore.
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(GitIgnore), 0o644)
	final := filepath.Join(dir, File)
	tmp := final + ".tmp"
	os.Remove(tmp)
	db, err := sql.Open("sqlite", tmp)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF;" + schema); err != nil {
		return "", err
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	if err := insertAll(tx, g, meta, states); err != nil {
		tx.Rollback()
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	db.Close()
	return final, os.Rename(tmp, final)
}

// FileState is what incremental indexing compares to spot changed files.
type FileState struct {
	Path        string
	Size, MTime int64
	Lang        string
}

func insertAll(tx *sql.Tx, g *model.Graph, meta map[string]string, states []FileState) error {
	ins := func(q string, rows func(st *sql.Stmt) error) error {
		st, err := tx.Prepare(q)
		if err != nil {
			return err
		}
		defer st.Close()
		return rows(st)
	}
	steps := []error{
		ins("INSERT INTO meta VALUES(?,?)", func(st *sql.Stmt) error {
			for k, v := range meta {
				if _, err := st.Exec(k, v); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT OR IGNORE INTO files VALUES(?,?)", func(st *sql.Stmt) error {
			for _, f := range g.Files {
				if _, err := st.Exec(f.Path, f.Lines); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO decls VALUES(?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, d := range g.Decls {
				if _, err := st.Exec(d.File, d.Name, d.Kind, d.Line, b2i(d.Exported)); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO refs VALUES(?,?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, r := range g.Refs {
				if _, err := st.Exec(r.File, r.Line, r.From, r.ToFile, r.To, r.Kind); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO db_uses VALUES(?,?,?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, u := range g.DBUses {
				if _, err := st.Exec(u.File, u.Line, u.From, u.Type, u.Name, strings.Join(u.Ops, ","), u.Client); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO routes VALUES(?,?,?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, r := range g.Routes {
				if _, err := st.Exec(r.File, r.Method, r.Path, r.Line, r.HandlerFile, r.Handler, r.Guards); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO pages VALUES(?,?,?,?)", func(st *sql.Stmt) error {
			for _, pg := range g.Pages {
				if _, err := st.Exec(pg.File, pg.Path, pg.Line, pg.Component); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO route_calls VALUES(?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, c := range g.RouteCalls {
				if _, err := st.Exec(c.File, c.Line, c.From, c.Path, c.Method); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO sql_tables VALUES(?,?,?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, t := range g.Tables {
				if _, err := st.Exec(t.Schema, t.Name, b2i(t.View), b2i(t.Invoker), b2i(t.RLS), b2i(t.AnonGrant), t.Src); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO sql_policies VALUES(?,?,?,?,?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, p := range g.Policies {
				if _, err := st.Exec(p.Schema, p.Table, p.Name, p.Cmd, strings.Join(p.Roles, ","), b2i(p.Permissive), p.Using, p.Check, p.Src); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO sql_refs VALUES(?,?,?,?)", func(st *sql.Stmt) error {
			for _, r := range g.SQLRefs {
				if _, err := st.Exec(r.Kind, r.Name, r.To, r.Src); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO sql_functions VALUES(?,?,?,?,?,?,?,?)", func(st *sql.Stmt) error {
			for _, f := range g.Functions {
				if _, err := st.Exec(f.Schema, f.Name, f.Returns, b2i(f.SecDef), b2i(f.CallerScoped), strings.Join(f.Exec, ","), f.Src, f.Args); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT INTO buckets VALUES(?,?,?)", func(st *sql.Stmt) error {
			for _, b := range g.Buckets {
				if _, err := st.Exec(b.ID, b2i(b.Public), b.Src); err != nil {
					return err
				}
			}
			return nil
		}),
		ins("INSERT OR REPLACE INTO file_state VALUES(?,?,?,?)", func(st *sql.Stmt) error {
			for _, f := range states {
				if _, err := st.Exec(f.Path, f.Size, f.MTime, f.Lang); err != nil {
					return err
				}
			}
			return nil
		}),
	}
	for _, err := range steps {
		if err != nil {
			return err
		}
	}
	return nil
}

// Update applies an incremental index in place: rows of the given files and
// path prefixes are replaced with g's, the SQL schema and meta are replaced
// whole, and file_state is replaced with states.
func Update(path string, g *model.Graph, files, prefixes []string, meta map[string]string, states []FileState) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	del := func(q string, args ...any) error { _, err := tx.Exec(q, args...); return err }
	codeTables := []struct{ table, col string }{{"files", "path"}, {"decls", "file"}, {"refs", "file"}, {"db_uses", "file"}, {"routes", "file"}, {"pages", "file"}, {"route_calls", "file"}}
	for _, f := range files {
		for _, t := range codeTables {
			if err := del("DELETE FROM "+t.table+" WHERE "+t.col+" = ?", f); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	for _, p := range prefixes {
		for _, t := range codeTables {
			if err := del("DELETE FROM "+t.table+" WHERE substr("+t.col+", 1, ?) = ?", len(p), p); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	for _, t := range []string{"sql_tables", "sql_policies", "sql_functions", "sql_refs", "buckets", "meta", "file_state"} {
		if err := del("DELETE FROM " + t); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := insertAll(tx, g, meta, states); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// States reads file_state, or nil if the index predates it.
func States(db *sql.DB) map[string]FileState {
	rows, err := db.Query("SELECT path,size,mtime,lang FROM file_state")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]FileState{}
	for rows.Next() {
		var f FileState
		rows.Scan(&f.Path, &f.Size, &f.MTime, &f.Lang)
		out[f.Path] = f
	}
	return out
}

// Find walks up from dir to the nearest .codumi/codumi.db.
func Find(dir string) (root, path string, ok bool) {
	d, _ := filepath.Abs(dir)
	for {
		if _, err := os.Stat(filepath.Join(d, legacyDir, legacyFile)); err == nil {
			migrateProject(d)
		}
		p := filepath.Join(d, Dir, File)
		if _, err := os.Stat(p); err == nil {
			return d, p, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", "", false
		}
		d = parent
	}
}

// Open opens an index read-only.
func Open(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro")
}

// LoadGraph reads the SQL side of the index back into a Graph (for audit).
func LoadGraph(db *sql.DB) (*model.Graph, error) {
	g := &model.Graph{}
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, ",")
	}
	rows, err := db.Query("SELECT schema,name,is_view,invoker,rls,anon_grant,src FROM sql_tables")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t model.Table
		var v, inv, rls, ag int
		rows.Scan(&t.Schema, &t.Name, &v, &inv, &rls, &ag, &t.Src)
		t.View, t.Invoker, t.RLS, t.AnonGrant = v == 1, inv == 1, rls == 1, ag == 1
		g.Tables = append(g.Tables, t)
	}
	rows.Close()
	rows, err = db.Query("SELECT schema,table_name,name,cmd,roles,permissive,using_expr,check_expr,src FROM sql_policies")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p model.Policy
		var r string
		var perm int
		rows.Scan(&p.Schema, &p.Table, &p.Name, &p.Cmd, &r, &perm, &p.Using, &p.Check, &p.Src)
		p.Roles, p.Permissive = split(r), perm == 1
		g.Policies = append(g.Policies, p)
	}
	rows.Close()
	rows, err = db.Query("SELECT schema,name,returns,secdef,caller_scoped,exec_roles,src FROM sql_functions")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f model.Function
		var sd, cs int
		var ex string
		rows.Scan(&f.Schema, &f.Name, &f.Returns, &sd, &cs, &ex, &f.Src)
		f.SecDef, f.CallerScoped, f.Exec = sd == 1, cs == 1, split(ex)
		g.Functions = append(g.Functions, f)
	}
	rows.Close()
	rows, err = db.Query("SELECT id,public,src FROM buckets")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var b model.Bucket
		var pub int
		rows.Scan(&b.ID, &pub, &b.Src)
		b.Public = pub == 1
		g.Buckets = append(g.Buckets, b)
	}
	rows.Close()
	rows, err = db.Query("SELECT file,method,path,line,handler_file,handler,guards FROM routes")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r model.Route
		rows.Scan(&r.File, &r.Method, &r.Path, &r.Line, &r.HandlerFile, &r.Handler, &r.Guards)
		g.Routes = append(g.Routes, r)
	}
	rows.Close()
	rows, err = db.Query("SELECT file,line,from_name,type,name,ops,client FROM db_uses")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u model.DBUse
		var ops string
		rows.Scan(&u.File, &u.Line, &u.From, &u.Type, &u.Name, &ops, &u.Client)
		u.Ops = split(ops)
		g.DBUses = append(g.DBUses, u)
	}
	rows.Close()
	rows, err = db.Query("SELECT file,line,from_name,to_file,to_name,kind FROM refs WHERE kind IN ('call','new','ref')")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r model.Ref
		rows.Scan(&r.File, &r.Line, &r.From, &r.ToFile, &r.To, &r.Kind)
		g.Refs = append(g.Refs, r)
	}
	rows.Close()
	rows, err = db.Query("SELECT path,lines FROM files")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f model.File
		rows.Scan(&f.Path, &f.Lines)
		g.Files = append(g.Files, f)
	}
	rows.Close()
	return g, nil
}
