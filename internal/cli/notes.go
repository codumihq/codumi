package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/store"
)

// Module notes: a short human-written card per feature (decisions, gotchas,
// what's pending) in .codumi/notes/<slug>.md, committed with the code. When
// shown, Codumi adds a live section from the index (routes, tables, who
// depends on it), so that part never goes stale.

type note struct {
	Slug, Name, Description string
	Paths                   []string
	Body                    string
	File                    string
}

func notesDir(root string) string { return filepath.Join(root, store.Dir, "notes") }

func parseNote(path string) (*note, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	n := &note{Slug: strings.TrimSuffix(filepath.Base(path), ".md"), File: path}
	s := string(b)
	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---"); end >= 0 {
			front := s[4 : 4+end]
			s = strings.TrimLeft(s[4+end+4:], "\n")
			var listKey string
			for _, line := range strings.Split(front, "\n") {
				t := strings.TrimSpace(line)
				if t == "" || strings.HasPrefix(t, "#") {
					continue
				}
				if strings.HasPrefix(t, "- ") && listKey == "paths" {
					n.Paths = append(n.Paths, strings.Trim(strings.TrimSpace(t[2:]), `"'`))
					continue
				}
				k, v, ok := strings.Cut(t, ":")
				if !ok {
					continue
				}
				k, v = strings.TrimSpace(k), strings.TrimSpace(v)
				listKey = k
				switch k {
				case "name":
					n.Name = strings.Trim(v, `"'`)
				case "description":
					n.Description = strings.Trim(v, `"'`)
				case "paths":
					if strings.HasPrefix(v, "[") {
						for _, p := range strings.Split(strings.Trim(v, "[]"), ",") {
							if p = strings.Trim(strings.TrimSpace(p), `"'`); p != "" {
								n.Paths = append(n.Paths, p)
							}
						}
					}
				}
			}
		}
	}
	n.Body = strings.TrimSpace(s)
	if n.Name == "" {
		n.Name = n.Slug
	}
	return n, nil
}

func loadNotes(root string) []*note {
	files, _ := filepath.Glob(filepath.Join(notesDir(root), "*.md"))
	sort.Strings(files)
	var out []*note
	for _, f := range files {
		if n, err := parseNote(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// globRe turns src/lib/**/x*.ts into a regexp over slash paths.
func globRe(glob string) *regexp.Regexp {
	glob = strings.TrimPrefix(strings.TrimSpace(glob), "./")
	if strings.HasSuffix(glob, "/") {
		glob += "**"
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			if i+2 < len(glob) && glob[i+2] == '/' {
				b.WriteString("(?:.*/)?")
				i += 2
			} else {
				b.WriteString(".*")
				i++
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	// a bare directory also matches what's inside it
	if !strings.ContainsAny(glob, "*?") && filepath.Ext(glob) == "" {
		b.WriteString("(?:/.*)?")
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func (n *note) matcher() func(string) bool {
	var res []*regexp.Regexp
	for _, p := range n.Paths {
		res = append(res, globRe(p))
	}
	return func(path string) bool {
		for _, r := range res {
			if r.MatchString(path) {
				return true
			}
		}
		return false
	}
}

func cmdNotes(pos []string, flags map[string]string) error {
	root, db, err := notesRoot()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}
	ns := loadNotes(root)
	if len(ns) == 0 {
		fmt.Fprintln(out, "no module notes yet. Create one with:  codumi note new <slug> --paths=src/lib/payments/**,src/app/api/payments/**")
		return nil
	}
	files := indexedFiles(db)
	for _, n := range ns {
		count := 0
		if files != nil {
			m := n.matcher()
			for _, f := range files {
				if m(f) {
					count++
				}
			}
		}
		desc := n.Description
		if desc == "" {
			desc = "(no description)"
		}
		warn := ""
		if files != nil && count == 0 {
			warn = "  [paths match no files]"
		}
		fmt.Fprintf(out, "%-22s %s — %s  (%d %s)%s\n", n.Slug, n.Name, desc, count, plural(count, "file", "files"), warn)
	}
	return nil
}

func notesRoot() (string, *sql.DB, error) {
	cwd, _ := os.Getwd()
	if root, path, ok := store.Find(cwd); ok {
		db, err := store.Open(path)
		if err != nil {
			return root, nil, nil
		}
		return root, db, nil
	}
	top, err := gitOut("rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		top = cwd
	}
	return top, nil, nil
}

func indexedFiles(db *sql.DB) []string {
	if db == nil {
		return nil
	}
	rows, err := db.Query("SELECT path FROM files")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		out = append(out, p)
	}
	return out
}

func cmdNote(pos []string, flags map[string]string) error {
	if len(pos) > 0 && pos[0] == "new" {
		return cmdNoteNew(pos[1:], flags)
	}
	root, db, err := notesRoot()
	if err != nil {
		return err
	}
	if db != nil {
		defer db.Close()
	}
	ns := loadNotes(root)

	// codumi note --file=src/x.ts → the notes that cover that file
	if f := flags["file"]; f != "" {
		f = strings.TrimPrefix(filepath.ToSlash(f), "./")
		if filepath.IsAbs(f) {
			if r, err := filepath.Rel(root, f); err == nil {
				f = filepath.ToSlash(r)
			}
		}
		var hit []*note
		for _, n := range ns {
			if n.matcher()(f) {
				hit = append(hit, n)
			}
		}
		if len(hit) == 0 {
			fmt.Fprintf(out, "no module note covers %s\n", f)
			return nil
		}
		for i, n := range hit {
			if i > 0 {
				fmt.Fprintln(out, "\n"+strings.Repeat("─", 60)+"\n")
			}
			showNote(n, db, flags)
		}
		return nil
	}
	if len(pos) == 0 {
		return cmdNotes(nil, flags)
	}
	q := strings.ToLower(pos[0])
	for _, n := range ns {
		if strings.ToLower(n.Slug) == q || strings.ToLower(n.Name) == q {
			showNote(n, db, flags)
			return nil
		}
	}
	var sugg []string
	for _, n := range ns {
		if strings.Contains(strings.ToLower(n.Slug+" "+n.Name+" "+n.Description), q) {
			sugg = append(sugg, n.Slug)
		}
	}
	if len(sugg) > 0 {
		return fmt.Errorf("no note %q. Did you mean: %s", pos[0], strings.Join(sugg, ", "))
	}
	return fmt.Errorf("no note %q. `codumi notes` lists them; `codumi note new %s --paths=…` creates it", pos[0], pos[0])
}

func showNote(n *note, db *sql.DB, flags map[string]string) {
	rel := n.File
	if r, err := filepath.Rel(filepath.Dir(filepath.Dir(filepath.Dir(n.File))), n.File); err == nil {
		rel = filepath.ToSlash(r)
	}
	fmt.Fprintf(out, "# %s\n", n.Name)
	if n.Description != "" {
		fmt.Fprintf(out, "%s\n", n.Description)
	}
	fmt.Fprintf(out, "note: %s · paths: %s\n\n", rel, strings.Join(n.Paths, ", "))
	if n.Body != "" {
		fmt.Fprintln(out, n.Body)
		fmt.Fprintln(out)
	}
	if db == nil {
		fmt.Fprintln(out, "(no index: run `codumi index` to add the live section)")
		return
	}
	live(n, db)
}

// live prints what the index knows about the files a note covers.
func live(n *note, db *sql.DB) {
	m := n.matcher()
	inside := map[string]bool{}
	for _, f := range indexedFiles(db) {
		if m(f) {
			inside[f] = true
		}
	}
	fmt.Fprintf(out, "## Live (from the index)\n")
	if len(inside) == 0 {
		fmt.Fprintln(out, "the note's paths match no indexed files — update `paths:` in the note")
		return
	}
	fmt.Fprintf(out, "files: %d\n", len(inside))

	// routes
	var routes []string
	if rows, err := db.Query("SELECT method,path,file,line,handler FROM routes ORDER BY path,method"); err == nil {
		for rows.Next() {
			var method, path, file, handler string
			var line int
			rows.Scan(&method, &path, &file, &line, &handler)
			if inside[file] {
				routes = append(routes, fmt.Sprintf("  %-6s %s  %s:%d", method, path, file, line))
			}
		}
		rows.Close()
	}
	if len(routes) > 0 {
		fmt.Fprintf(out, "routes: %d\n%s\n", len(routes), strings.Join(limitLines(routes, 25), "\n"))
	}

	// database
	type agg struct {
		ops   map[string]bool
		n     int
		admin bool
	}
	dbs := map[string]*agg{}
	if rows, err := db.Query("SELECT file,type,name,ops,client FROM db_uses"); err == nil {
		for rows.Next() {
			var file, typ, name, ops, client string
			rows.Scan(&file, &typ, &name, &ops, &client)
			if !inside[file] {
				continue
			}
			k := typ + " " + name
			a := dbs[k]
			if a == nil {
				a = &agg{ops: map[string]bool{}}
				dbs[k] = a
			}
			a.n++
			a.admin = a.admin || bypassesRLS(client)
			for _, o := range strings.Split(ops, ",") {
				if o != "" {
					a.ops[o] = true
				}
			}
		}
		rows.Close()
	}
	if len(dbs) > 0 {
		var keys []string
		for k := range dbs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var lines []string
		for _, k := range keys {
			a := dbs[k]
			var ops []string
			for o := range a.ops {
				ops = append(ops, o)
			}
			sort.Strings(ops)
			admin := ""
			if a.admin {
				admin = "  [admin]"
			}
			lines = append(lines, fmt.Sprintf("  %-40s %-26s ×%d%s", k, strings.Join(ops, ","), a.n, admin))
		}
		fmt.Fprintf(out, "database: %d %s\n%s\n", len(keys), plural(len(keys), "object", "objects"), strings.Join(limitLines(lines, 30), "\n"))
	}

	// dependencies in and out
	usedBy := map[string]int{}
	dependsOn := map[string]int{}
	if rows, err := db.Query("SELECT file,to_file FROM refs WHERE kind != 'import' AND kind != 'export' AND to_file NOT LIKE 'ext:%'"); err == nil {
		for rows.Next() {
			var from, to string
			rows.Scan(&from, &to)
			switch {
			case !inside[from] && inside[to]:
				usedBy[from]++
			case inside[from] && !inside[to]:
				dependsOn[to]++
			}
		}
		rows.Close()
	}
	printTop := func(title string, m map[string]int) {
		if len(m) == 0 {
			return
		}
		type kv struct {
			k string
			v int
		}
		var xs []kv
		for k, v := range m {
			xs = append(xs, kv{k, v})
		}
		sort.Slice(xs, func(i, j int) bool {
			if xs[i].v != xs[j].v {
				return xs[i].v > xs[j].v
			}
			return xs[i].k < xs[j].k
		})
		fmt.Fprintf(out, "%s: %d %s\n", title, len(xs), plural(len(xs), "file", "files"))
		for i, x := range xs {
			if i == 12 {
				fmt.Fprintf(out, "  … %d more\n", len(xs)-12)
				break
			}
			fmt.Fprintf(out, "  %-60s ×%d\n", x.k, x.v)
		}
	}
	printTop("used by (outside the module)", usedBy)
	printTop("depends on", dependsOn)
}

func limitLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return append(lines[:n:n], fmt.Sprintf("  … %d more", len(lines)-n))
}

var reSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func cmdNoteNew(pos []string, flags map[string]string) error {
	if len(pos) == 0 || flags["paths"] == "" {
		return errors.New("usage: codumi note new <slug> --paths=src/lib/payments/**,src/app/api/payments/** [--name=\"Payments\"] [--description=\"…\"]")
	}
	slug := strings.ToLower(pos[0])
	if !reSlug.MatchString(slug) {
		return errors.New("slug must be lowercase letters, digits and dashes, like payments or order-flow")
	}
	root, db, _ := notesRoot()
	if db != nil {
		defer db.Close()
	}
	dir := notesDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ensureNotesTracked(root)
	path := filepath.Join(dir, slug+".md")
	if exists(path) {
		return fmt.Errorf("%s already exists", path)
	}
	name := flags["name"]
	if name == "" {
		name = strings.ReplaceAll(slug, "-", " ")
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	var paths []string
	for _, p := range strings.Split(flags["paths"], ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, "  - "+p)
		}
	}
	content := fmt.Sprintf(`---
name: %s
description: %s
paths:
%s
---

## What it does

## Decisions
<!-- Choices that aren't obvious from the code, and why. -->

## Gotchas
<!-- Things that broke before, order of operations, caches to clear… -->

## Pending
`, name, flags["description"], strings.Join(paths, "\n"))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	rel, _ := filepath.Rel(root, path)
	fmt.Fprintf(out, "created %s — fill in the sections; `codumi note %s` shows it with the live part\n", filepath.ToSlash(rel), slug)
	return nil
}

// ensureNotesTracked lets git see .codumi/notes while the index stays ignored.
func ensureNotesTracked(root string) {
	p := filepath.Join(root, store.Dir, ".gitignore")
	os.WriteFile(p, []byte(store.GitIgnore), 0o644)
}
