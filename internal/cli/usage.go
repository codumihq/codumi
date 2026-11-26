package cli

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/codumihq/codumi/internal/sqlschema"
	"github.com/codumihq/codumi/internal/store"
)

// Usage log: every MCP call is recorded locally (never sent anywhere) with
// the bytes Codumi returned and a conservative estimate of what an agent
// would read without it, so `codumi stats` and the desktop app can show the
// savings. Estimates follow the method in the README ("Measured on a real
// app"): grep output for uses, the route file plus its direct imports for
// routes, the migrations for the audit. Tools with no fair baseline (notes,
// sql, index) claim no savings.

const usageSchema = `CREATE TABLE IF NOT EXISTS calls(ts INT, project TEXT, tool TEXT, arg TEXT, out_bytes INT, est_bytes INT, ms INT);
CREATE INDEX IF NOT EXISTS calls_ts ON calls(ts);`

var (
	usageMu      sync.Mutex
	pendingUsage sync.WaitGroup // MCP waits for these before exiting
)

func usagePath() string {
	if p := os.Getenv("CODUMI_USAGE_DB"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(store.UserDir(dir), "usage.db")
}

func openUsage() (*sql.DB, error) {
	p := usagePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", p)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(usageSchema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// logUsage records one tool call. It runs after the answer was sent.
func logUsage(root, tool string, args map[string]any, outBytes int, took time.Duration) {
	est := int64(outBytes)
	if _, idx, ok := store.Find(root); ok {
		if db, err := store.Open(idx); err == nil {
			if e := estimate(root, db, tool, args); e > 0 {
				est = e
			}
			db.Close()
		}
	}
	arg := str(args, "name")
	if arg == "" {
		arg = str(args, "path")
	}
	if arg == "" {
		arg = str(args, "file")
	}
	usageMu.Lock()
	defer usageMu.Unlock()
	db, err := openUsage()
	if err != nil {
		return
	}
	defer db.Close()
	db.Exec("INSERT INTO calls VALUES(?,?,?,?,?,?,?)", time.Now().Unix(), root, strings.TrimPrefix(tool, "codumi_"), arg, outBytes, est, took.Milliseconds())
}

// estimate returns the bytes an agent would plausibly read without Codumi,
// or 0 when there is no fair baseline.
func estimate(root string, db *sql.DB, tool string, args map[string]any) int64 {
	switch tool {
	case "codumi_refs":
		name := strings.TrimSuffix(str(args, "name"), "()")
		if i := strings.LastIndex(name, ":"); i >= 0 {
			name = name[i+1:]
		}
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		return grepBytes(root, indexedFiles(db), name)
	case "codumi_table":
		name := strings.ToLower(strings.TrimSuffix(str(args, "name"), "()"))
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		files := indexedFiles(db)
		for _, m := range sqlschema.FindMigrations(root) {
			if r, err := filepath.Rel(root, m); err == nil {
				files = append(files, filepath.ToSlash(r))
			}
		}
		return grepBytes(root, files, name)
	case "codumi_route":
		return routeBaseline(root, db, str(args, "path"))
	case "codumi_page":
		return pageBaseline(root, db, str(args, "path"))
	case "codumi_find":
		return grepLiteral(root, indexedFiles(db), str(args, "text"))
	case "codumi_audit":
		var n int64
		for _, m := range sqlschema.FindMigrations(root) {
			if st, err := os.Stat(m); err == nil {
				n += st.Size()
			}
		}
		return n
	}
	return 0
}

// grepBytes is the size of `grep -rnw name` over files: path:line:text per hit.
func grepBytes(root string, files []string, name string) int64 {
	if name == "" {
		return 0
	}
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		return 0
	}
	needle := []byte(name)
	var n int64
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || !bytes.Contains(b, needle) {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		line := 0
		for sc.Scan() {
			line++
			if re.Match(sc.Bytes()) {
				n += int64(len(f) + len(strconv.Itoa(line)) + 3 + len(sc.Bytes()))
			}
		}
	}
	return n
}

// routeBaseline: the route's file and handler file plus the project files
// they reference directly.
func routeBaseline(root string, db *sql.DB, path string) int64 {
	q := strings.TrimSuffix(strings.SplitN(path, "?", 2)[0], "/")
	rows, err := db.Query(`SELECT file, handler_file, path FROM routes`)
	if err != nil {
		return 0
	}
	files := map[string]bool{}
	var partial []string
	for rows.Next() {
		var f, hf, p string
		rows.Scan(&f, &hf, &p)
		switch {
		case strings.TrimSuffix(p, "/") == q || matchRoute(p, q):
			files[f], files[hf] = true, true
		case strings.Contains(p, q):
			partial = append(partial, f, hf)
		}
	}
	rows.Close()
	if len(files) == 0 {
		for _, f := range partial {
			files[f] = true
		}
	}
	direct := map[string]bool{}
	for f := range files {
		if f == "" {
			continue
		}
		direct[f] = true
		r, err := db.Query(`SELECT DISTINCT to_file FROM refs WHERE file=? AND to_file NOT LIKE 'ext:%'`, f)
		if err != nil {
			continue
		}
		for r.Next() {
			var t string
			r.Scan(&t)
			direct[t] = true
		}
		r.Close()
	}
	var n int64
	for f := range direct {
		if st, err := os.Stat(filepath.Join(root, f)); err == nil {
			n += st.Size()
		}
	}
	return n
}

// ---- codumi stats ------------------------------------------------------------

type statRow struct {
	Key      string  `json:"key"`
	Calls    int     `json:"calls"`
	Returned int64   `json:"tokens_returned"`
	Without  int64   `json:"tokens_without_estimate"`
	Ratio    float64 `json:"ratio"`
}

func tok(b int64) int64 { return b / 4 }

func human(t int64) string {
	switch {
	case t >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(t)/1e6)
	case t >= 1_000:
		return fmt.Sprintf("%.1fk", float64(t)/1e3)
	}
	return strconv.FormatInt(t, 10)
}

func cmdStats(pos []string, flags map[string]string) error {
	days := intFlag(flags, "days", 30)
	db, err := openUsage()
	if err != nil {
		return err
	}
	defer db.Close()
	since := time.Now().AddDate(0, 0, -days).Unix()
	q := "SELECT ts, project, tool, out_bytes, est_bytes FROM calls WHERE ts >= ?"
	qargs := []any{since}
	if p := flags["project"]; p != "" {
		q += " AND project LIKE ?"
		qargs = append(qargs, "%"+p+"%")
	}
	rows, err := db.Query(q, qargs...)
	if err != nil {
		return err
	}
	byTool, byProject, byDay := map[string]*statRow{}, map[string]*statRow{}, map[string]*statRow{}
	total := &statRow{Key: "total"}
	add := func(m map[string]*statRow, k string, out, est int64) {
		r := m[k]
		if r == nil {
			r = &statRow{Key: k}
			m[k] = r
		}
		r.Calls++
		r.Returned += tok(out)
		r.Without += tok(est)
	}
	for rows.Next() {
		var ts, out, est int64
		var project, tool string
		rows.Scan(&ts, &project, &tool, &out, &est)
		add(byTool, tool, out, est)
		add(byProject, filepath.Base(project), out, est)
		add(byDay, time.Unix(ts, 0).Format("2006-01-02"), out, est)
		total.Calls++
		total.Returned += tok(out)
		total.Without += tok(est)
	}
	rows.Close()
	finish := func(m map[string]*statRow) []*statRow {
		var xs []*statRow
		for _, r := range m {
			if r.Returned > 0 {
				r.Ratio = float64(r.Without) / float64(r.Returned)
			}
			xs = append(xs, r)
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].Without-xs[i].Returned > xs[j].Without-xs[j].Returned })
		return xs
	}
	if total.Returned > 0 {
		total.Ratio = float64(total.Without) / float64(total.Returned)
	}
	tools, projects, dayRows := finish(byTool), finish(byProject), finish(byDay)
	sort.Slice(dayRows, func(i, j int) bool { return dayRows[i].Key > dayRows[j].Key })

	if flags["json"] == "true" {
		return printJSON(map[string]any{"days": days, "total": total, "by_tool": tools, "by_project": projects, "by_day": dayRows})
	}
	if total.Calls == 0 {
		fmt.Fprintf(out, "no Codumi calls from agents in the last %d days (only MCP calls are counted)\n", days)
		return nil
	}
	fmt.Fprintf(out, "last %d days: %d %s from agents\n", days, total.Calls, plural(total.Calls, "call", "calls"))
	fmt.Fprintf(out, "context returned: %s tokens · estimated without Codumi: %s · %.1f× less (%s tokens saved)\n",
		human(total.Returned), human(total.Without), total.Ratio, human(total.Without-total.Returned))
	section := func(title string, xs []*statRow, limit int) {
		fmt.Fprintf(out, "\n%s\n", title)
		for i, r := range xs {
			if limit > 0 && i == limit {
				break
			}
			fmt.Fprintf(out, "  %-24s %5d %-5s %9s → %-9s %5.1f×\n", r.Key, r.Calls, plural(r.Calls, "call", "calls"), human(r.Without), human(r.Returned), r.Ratio)
		}
	}
	section("by tool (without → with)", tools, 0)
	section("by project", projects, 10)
	section("by day", dayRows, 7)
	fmt.Fprintln(out, "\nestimates are conservative: grep output for uses, route file + direct imports for routes, migrations for the audit; notes and sql claim nothing")
	return nil
}

// grepLiteral is the size of `grep -rni text` over files.
func grepLiteral(root string, files []string, text string) int64 {
	low := strings.ToLower(strings.TrimSpace(text))
	if low == "" {
		return 0
	}
	var n int64
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil || !strings.Contains(strings.ToLower(string(b)), low) {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(strings.ToLower(line), low) {
				n += int64(len(f) + len(strconv.Itoa(i+1)) + 3 + len(line))
			}
		}
	}
	return n
}

// pageBaseline: the files of the page's component tree, which an agent
// would otherwise open one by one.
func pageBaseline(root string, db *sql.DB, path string) int64 {
	q := strings.TrimSpace(path)
	if q != "/" {
		q = strings.TrimSuffix(q, "/")
	}
	ps := findPages(db, q)
	if len(ps) != 1 {
		return 0
	}
	nodes := map[[2]string]bool{}
	treeNodes(componentTree(db, ps[0].File, ps[0].Component, 3), nodes)
	files := map[string]bool{}
	for k := range nodes {
		files[k[0]] = true
	}
	var n int64
	for f := range files {
		if st, err := os.Stat(filepath.Join(root, f)); err == nil {
			n += st.Size()
		}
	}
	return n
}

// UsageSince sums the Codumi calls logged for a project since a moment
// (the desktop chat shows it under each answer).
func UsageSince(project string, since int64) (calls int, returned, without int64) {
	db, err := openUsage()
	if err != nil {
		return
	}
	defer db.Close()
	db.QueryRow(`SELECT count(*), COALESCE(sum(out_bytes),0), COALESCE(sum(est_bytes),0) FROM calls WHERE ts >= ? AND project = ?`, since, project).Scan(&calls, &returned, &without)
	return calls, returned / 4, without / 4
}
