package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/codumihq/codumi/internal/audit"
	"github.com/codumihq/codumi/internal/store"
)

// out receives command output and notes receives side notes. The CLI
// points them at stdout/stderr; the MCP server captures both.
var (
	out   io.Writer = os.Stdout
	notes io.Writer = os.Stderr
)

// ---- index ----------------------------------------------------------------

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ---- shared ---------------------------------------------------------------

func openIndex() (string, *sql.DB, error) {
	cwd, _ := os.Getwd()
	root, path, ok := store.Find(cwd)
	if !ok {
		return "", nil, errors.New("no index found here or in a parent folder; run `codumi index` in the project root first")
	}
	db, err := store.Open(path)
	if err != nil {
		return "", nil, err
	}
	var at string
	db.QueryRow("SELECT value FROM meta WHERE key='indexed_at'").Scan(&at)
	if ts, err := strconv.ParseInt(at, 10, 64); err == nil {
		if st, err := os.Stat(filepath.Join(root, ".git", "index")); err == nil && st.ModTime().Unix() > ts+5 {
			fmt.Fprintln(notes, "note: the repo changed after the last index; run `codumi index` for fresh results")
		}
	}
	return root, db, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func intFlag(flags map[string]string, k string, def int) int {
	if v, ok := flags[k]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func bypassesRLS(client string) bool {
	c := strings.ToLower(client)
	return strings.Contains(c, "admin") || strings.Contains(c, "service")
}

// ---- refs -----------------------------------------------------------------

type declRow struct {
	File, Name, Kind string
	Line             int
	Exported         bool
}

type refRow struct {
	File string `json:"file"`
	Line int    `json:"line"`
	From string `json:"from"`
	Kind string `json:"kind"`
}

func findDecls(db *sql.DB, q string) ([]declRow, error) {
	file := ""
	if i := strings.LastIndex(q, ":"); i > 0 {
		file, q = q[:i], q[i+1:]
	}
	q = strings.TrimSuffix(q, "()")
	rows, err := db.Query(`SELECT file,name,kind,line,exported FROM decls WHERE name = ? OR name LIKE ? ORDER BY exported DESC, file`, q, "%."+q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []declRow
	for rows.Next() {
		var d declRow
		var ex int
		rows.Scan(&d.File, &d.Name, &d.Kind, &d.Line, &ex)
		d.Exported = ex == 1
		if file != "" && !strings.HasSuffix(d.File, file) && !strings.Contains(d.File, file) {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func cmdRefs(pos []string, flags map[string]string) error {
	if len(pos) == 0 {
		return errors.New("usage: codumi refs <name>   (name, Class.member or path/file.ts:name)")
	}
	_, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	decls, err := findDecls(db, pos[0])
	if err != nil {
		return err
	}
	if len(decls) == 0 {
		var sugg []string
		rows, _ := db.Query(`SELECT DISTINCT name FROM decls WHERE name LIKE ? LIMIT 8`, "%"+strings.TrimSuffix(pos[0], "()")+"%")
		for rows != nil && rows.Next() {
			var s string
			rows.Scan(&s)
			sugg = append(sugg, s)
		}
		if len(sugg) > 0 {
			return fmt.Errorf("no declaration named %q. Similar: %s", pos[0], strings.Join(sugg, ", "))
		}
		return fmt.Errorf("no declaration named %q", pos[0])
	}
	all := flags["all"] == "true"
	limit := intFlag(flags, "limit", 80)
	type result struct {
		Decl   string         `json:"declaration"`
		At     string         `json:"at"`
		Kind   string         `json:"kind"`
		Counts map[string]int `json:"counts"`
		Refs   []refRow       `json:"refs"`
	}
	var results []result
	for _, d := range decls {
		rows, err := db.Query(`SELECT file,line,from_name,kind FROM refs WHERE to_name=? AND to_file=? ORDER BY file,line`, d.Name, d.File)
		if err != nil {
			return err
		}
		r := result{Decl: d.Name, At: fmt.Sprintf("%s:%d", d.File, d.Line), Kind: d.Kind, Counts: map[string]int{}}
		for rows.Next() {
			var x refRow
			rows.Scan(&x.File, &x.Line, &x.From, &x.Kind)
			r.Counts[x.Kind]++
			if !all && (x.Kind == "import" || x.Kind == "export") {
				continue
			}
			r.Refs = append(r.Refs, x)
		}
		rows.Close()
		results = append(results, r)
	}
	if flags["json"] == "true" {
		return printJSON(results)
	}
	printed := 0
	for i, r := range results {
		if i > 0 {
			fmt.Fprintln(out)
		}
		files := map[string]bool{}
		for _, x := range r.Refs {
			files[x.File] = true
		}
		var parts []string
		for k, v := range r.Counts {
			parts = append(parts, fmt.Sprintf("%s %d", k, v))
		}
		sort.Strings(parts)
		fmt.Fprintf(out, "%s  [%s]  %s\n", r.Decl, r.Kind, r.At)
		fmt.Fprintf(out, "%d %s in %d %s (%s)\n", len(r.Refs), plural(len(r.Refs), "use", "uses"), len(files), plural(len(files), "file", "files"), strings.Join(parts, ", "))
		last := ""
		for _, x := range r.Refs {
			if limit > 0 && printed >= limit {
				fmt.Fprintf(out, "… %d more (use --limit=0 for all, or --json)\n", len(r.Refs)-countUpTo(r.Refs, x))
				break
			}
			if x.File != last {
				fmt.Fprintln(out, x.File)
				last = x.File
			}
			fmt.Fprintf(out, "  %-6d %-28s %s\n", x.Line, x.From, x.Kind)
			printed++
		}
	}
	return nil
}

func countUpTo(refs []refRow, x refRow) int {
	for i, r := range refs {
		if r == x {
			return i
		}
	}
	return len(refs)
}

// ---- table ----------------------------------------------------------------

func cmdTable(pos []string, flags map[string]string) error {
	if len(pos) == 0 {
		return errors.New("usage: codumi table <table | view | rpc function | bucket>")
	}
	_, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	name := strings.ToLower(strings.TrimSuffix(pos[0], "()"))
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	tableJSON := map[string]any{}
	found := false

	type use struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		From   string `json:"from"`
		Ops    string `json:"ops"`
		Client string `json:"client"`
	}
	codeUses := func(typ string) []use {
		rows, err := db.Query(`SELECT file,line,from_name,ops,client FROM db_uses WHERE type=? AND lower(name)=? ORDER BY file,line`, typ, name)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var us []use
		for rows.Next() {
			var u use
			rows.Scan(&u.File, &u.Line, &u.From, &u.Ops, &u.Client)
			us = append(us, u)
		}
		return us
	}
	printUses := func(us []use) {
		files := map[string]bool{}
		for _, u := range us {
			files[u.File] = true
		}
		fmt.Fprintf(out, "code: %d %s in %d %s\n", len(us), plural(len(us), "use", "uses"), len(files), plural(len(files), "file", "files"))
		for _, u := range us {
			tag := ""
			if bypassesRLS(u.Client) {
				tag = "  [admin client, bypasses RLS]"
			}
			fmt.Fprintf(out, "  %s:%d  %s  %s%s\n", u.File, u.Line, u.From, u.Ops, tag)
		}
	}
	type pol struct {
		Name, Cmd, Roles, Using, Check, Src string
		Permissive                          bool
	}
	policies := func(schema, table, mention string) []pol {
		q := `SELECT name,cmd,roles,permissive,using_expr,check_expr,src FROM sql_policies WHERE schema=? AND table_name=?`
		args := []any{schema, table}
		if mention != "" {
			q += ` AND (using_expr LIKE ? OR check_expr LIKE ?)`
			args = append(args, "%'"+mention+"'%", "%'"+mention+"'%")
		}
		rows, err := db.Query(q+" ORDER BY src", args...)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var ps []pol
		for rows.Next() {
			var p pol
			var perm int
			rows.Scan(&p.Name, &p.Cmd, &p.Roles, &perm, &p.Using, &p.Check, &p.Src)
			p.Permissive = perm == 1
			ps = append(ps, p)
		}
		return ps
	}
	printPolicies := func(ps []pol) {
		fmt.Fprintf(out, "policies: %d\n", len(ps))
		for _, p := range ps {
			kind := ""
			if !p.Permissive {
				kind = " restrictive"
			}
			fmt.Fprintf(out, "  %-6s %-22s \"%s\"%s\n", p.Cmd, p.Roles, p.Name, kind)
			if p.Using != "" {
				fmt.Fprintf(out, "         using %s\n", clip(p.Using, 160))
			}
			if p.Check != "" {
				fmt.Fprintf(out, "         check %s\n", clip(p.Check, 160))
			}
			fmt.Fprintf(out, "         %s\n", p.Src)
		}
	}

	// table / view
	rows, err := db.Query(`SELECT schema,name,is_view,invoker,rls,anon_grant,src FROM sql_tables WHERE lower(name)=?`, name)
	if err != nil {
		return err
	}
	type tbl struct {
		Schema, Name, Src             string
		View, Invoker, RLS, AnonGrant bool
	}
	var tables []tbl
	for rows.Next() {
		var t tbl
		var v, inv, rls, ag int
		rows.Scan(&t.Schema, &t.Name, &v, &inv, &rls, &ag, &t.Src)
		t.View, t.Invoker, t.RLS, t.AnonGrant = v == 1, inv == 1, rls == 1, ag == 1
		tables = append(tables, t)
	}
	rows.Close()
	tableUses := codeUses("table")
	for _, t := range tables {
		found = true
		ps := policies(t.Schema, t.Name, "")
		if flags["json"] == "true" {
			tableJSON["table"] = map[string]any{"schema": t.Schema, "name": t.Name, "view": t.View, "rls": t.RLS, "anon_grant": t.AnonGrant, "src": t.Src, "policies": ps, "uses": tableUses}
			continue
		}
		kind, state := "table", "RLS on"
		if t.View {
			kind, state = "view", "security definer (ignores RLS)"
			if t.Invoker {
				state = "security invoker"
			}
		} else if !t.RLS {
			state = "RLS OFF"
		}
		if !t.AnonGrant {
			state += ", anon revoked"
		}
		fmt.Fprintf(out, "%s %s.%s  (%s)  %s\n", kind, t.Schema, t.Name, state, t.Src)
		if !t.View {
			printPolicies(ps)
		}
		printUses(tableUses)
		fmt.Fprintln(out)
	}
	if len(tables) == 0 && len(tableUses) > 0 {
		found = true
		if flags["json"] == "true" {
			tableJSON["table"] = map[string]any{"name": name, "in_migrations": false, "uses": tableUses}
		} else {
			fmt.Fprintf(out, "table %s  (not created by any migration found)\n", name)
			printUses(tableUses)
			fmt.Fprintln(out)
		}
	}

	// rpc function
	rows, err = db.Query(`SELECT schema,name,returns,secdef,exec_roles,src,args FROM sql_functions WHERE lower(name)=?`, name)
	if err != nil {
		return err
	}
	for rows.Next() {
		found = true
		var schema, fname, ret, exec, src, fargs string
		var sd int
		rows.Scan(&schema, &fname, &ret, &sd, &exec, &src, &fargs)
		us := codeUses("rpc")
		type sqlUse struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
			Src  string `json:"src"`
			Via  string `json:"via,omitempty"`
		}
		var sqlUses []sqlUse
		if r, err := db.Query(`SELECT kind,name,src FROM sql_refs WHERE to_name=? ORDER BY kind,name`, fname); err == nil {
			for r.Next() {
				var u sqlUse
				r.Scan(&u.Kind, &u.Name, &u.Src)
				sqlUses = append(sqlUses, u)
			}
			r.Close()
		}
		// A calling function that is itself fired by triggers: say which.
		for i, u := range sqlUses {
			if u.Kind != "function" {
				continue
			}
			caller := strings.TrimSuffix(u.Name[strings.LastIndex(u.Name, ".")+1:], "()")
			if r, err := db.Query(`SELECT name FROM sql_refs WHERE kind='trigger' AND to_name=? ORDER BY name`, caller); err == nil {
				var ts []string
				for r.Next() {
					var n string
					r.Scan(&n)
					ts = append(ts, n)
				}
				r.Close()
				if len(ts) > 3 {
					ts = append(ts[:3], fmt.Sprintf("+%d more", len(ts)-3))
				}
				if len(ts) > 0 {
					sqlUses[i].Via = "trigger " + strings.Join(ts, ", ")
				}
			}
		}
		if flags["json"] == "true" {
			tableJSON["function"] = map[string]any{"schema": schema, "name": fname, "returns": ret, "security_definer": sd == 1, "args": fargs, "execute": strings.Split(exec, ","), "src": src, "uses": us, "database_uses": sqlUses}
			continue
		}
		mode := "security invoker"
		if sd == 1 {
			mode = "SECURITY DEFINER (bypasses RLS)"
		}
		fmt.Fprintf(out, "function %s.%s(%s) → %s  (%s)  %s\n", schema, fname, fargs, ret, mode, src)
		fmt.Fprintf(out, "execute: %s\n", exec)
		fmt.Fprintf(out, "database: %d %s (functions, triggers and policies that call it)\n", len(sqlUses), plural(len(sqlUses), "use", "uses"))
		for _, u := range sqlUses {
			via := ""
			if u.Via != "" {
				via = "  ← " + u.Via
			}
			fmt.Fprintf(out, "  %-8s %s  %s%s\n", u.Kind, u.Name, u.Src, via)
		}
		printUses(us)
		fmt.Fprintln(out)
	}
	rows.Close()

	// bucket
	rows, err = db.Query(`SELECT id,public,src FROM buckets WHERE lower(id)=?`, name)
	if err != nil {
		return err
	}
	for rows.Next() {
		found = true
		var id, src string
		var pub int
		rows.Scan(&id, &pub, &src)
		ps := policies("storage", "objects", id)
		us := codeUses("bucket")
		if flags["json"] == "true" {
			tableJSON["bucket"] = map[string]any{"id": id, "public": pub == 1, "src": src, "policies": ps, "uses": us}
			continue
		}
		vis := "private"
		if pub == 1 {
			vis = "PUBLIC (files readable by URL)"
		}
		fmt.Fprintf(out, "bucket %s  (%s)  %s\n", id, vis, src)
		printPolicies(ps)
		printUses(us)
		fmt.Fprintln(out)
	}
	rows.Close()

	if !found {
		return fmt.Errorf("no table, view, function or bucket named %q", pos[0])
	}
	if flags["json"] == "true" {
		return printJSON(tableJSON)
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- audit ----------------------------------------------------------------

func cmdAudit(pos []string, flags map[string]string) error {
	root, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	g, err := store.LoadGraph(db)
	if err != nil {
		return err
	}
	fs := audit.Run(root, g)
	if m, ok := map[string]audit.Severity{"high": audit.High, "medium": audit.Medium, "low": audit.Low}[flags["min"]]; ok {
		kept := fs[:0]
		for _, f := range fs {
			if f.Severity >= m {
				kept = append(kept, f)
			}
		}
		fs = kept
	}
	failAt := -1
	switch flags["fail-on"] {
	case "high":
		failAt = int(audit.High)
	case "medium":
		failAt = int(audit.Medium)
	case "low":
		failAt = int(audit.Low)
	}
	fail := false
	for _, f := range fs {
		if failAt >= 0 && int(f.Severity) >= failAt {
			fail = true
		}
	}
	if flags["json"] == "true" {
		if err := printJSON(fs); err != nil {
			return err
		}
	} else {
		counts := map[audit.Severity]int{}
		for _, f := range fs {
			counts[f.Severity]++
		}
		fmt.Fprintf(out, "codumi audit: %d high, %d medium, %d low\n", counts[audit.High], counts[audit.Medium], counts[audit.Low])
		if len(g.Tables) == 0 && len(g.Functions) == 0 {
			fmt.Fprintln(out, "note: no SQL migrations were indexed, so only code checks ran")
		}
		for _, f := range fs {
			h := ""
			if f.Heuristic {
				h = "  (heuristic)"
			}
			fmt.Fprintf(out, "\n%-6s %s  %s%s\n", f.Level, f.Rule, f.Target, h)
			fmt.Fprintf(out, "       %s\n", f.Message)
			fmt.Fprintf(out, "       at   %s\n", f.Where)
			if f.Fix != "" {
				fmt.Fprintf(out, "       fix  %s\n", f.Fix)
			}
			if len(f.UsedIn) > 0 {
				more := ""
				if len(f.UsedIn) > 1 {
					more = fmt.Sprintf(" (+%d more)", len(f.UsedIn)-1)
				}
				fmt.Fprintf(out, "       used %s%s\n", f.UsedIn[0], more)
			}
		}
	}
	if fail {
		return exitErr(2)
	}
	return nil
}

// ---- sql ------------------------------------------------------------------

func cmdSQL(pos []string, flags map[string]string) error {
	if len(pos) == 0 {
		return errors.New(`usage: codumi sql "SELECT ..."   tables: files, decls, refs, db_uses, routes, route_calls, sql_tables, sql_policies, sql_functions, buckets`)
	}
	q := strings.Join(pos, " ")
	if err := readOnlySQL(q); err != nil {
		return err
	}
	_, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	fmt.Fprintln(out, strings.Join(cols, "\t"))
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		rows.Scan(ptrs...)
		parts := make([]string, len(cols))
		for i, v := range vals {
			switch x := v.(type) {
			case []byte:
				parts[i] = string(x)
			case nil:
				parts[i] = ""
			default:
				parts[i] = fmt.Sprint(x)
			}
		}
		fmt.Fprintln(out, strings.Join(parts, "\t"))
	}
	return rows.Err()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

var (
	sqlComment  = regexp.MustCompile(`(?s)/\*.*?\*/|--[^\n]*`)
	sqlStart    = regexp.MustCompile(`(?i)^\s*(select|with|explain|values)\b`)
	sqlBlocked  = regexp.MustCompile(`(?i)\b(attach|detach|vacuum|pragma|load_extension|insert|update|delete|replace|create|drop|alter|reindex|analyze)\b`)
	sqlLiterals = regexp.MustCompile(`'(?:[^']|'')*'|"(?:[^"]|"")*"`)
)

// readOnlySQL deixa passar só uma consulta de leitura no índice. O banco já abre
// em modo leitura, mas ATTACH leria qualquer arquivo SQLite da máquina e VACUUM
// INTO gravaria um arquivo novo; um agente enganado por um texto malicioso
// (prompt injection) poderia pedir isso pela ferramenta codumi_sql.
func readOnlySQL(q string) error {
	code := sqlLiterals.ReplaceAllString(sqlComment.ReplaceAllString(q, " "), "''")
	if strings.Contains(strings.TrimRight(strings.TrimSpace(code), ";"), ";") {
		return errors.New("sql: one statement at a time")
	}
	if !sqlStart.MatchString(code) {
		return errors.New("sql: only read queries (SELECT, WITH, EXPLAIN, VALUES) run on the index")
	}
	if m := sqlBlocked.FindString(code); m != "" {
		return fmt.Errorf("sql: %q is not allowed; the index is read-only", strings.ToUpper(m))
	}
	return nil
}
