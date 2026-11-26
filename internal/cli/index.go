package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codumihq/codumi/internal/extract"
	"github.com/codumihq/codumi/internal/model"
	"github.com/codumihq/codumi/internal/sqlschema"
	"github.com/codumihq/codumi/internal/store"
)

// schemaVersion changes whenever the index layout or extraction changes in a
// way old rows can't be mixed with new ones; a mismatch forces a full index.
const schemaVersion = "8" // sobe quando os extratores mudam: força reindexar tudo

var (
	tsExts      = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".mts": true, ".cts": true}
	configs     = map[string]bool{"tsconfig.json": true, "jsconfig.json": true, "package.json": true}
	skipDirs    = map[string]bool{"node_modules": true, "dist": true, "build": true, "out": true, "coverage": true, "vendor": true, "graphify-out": true, "testdata": true, "target": true, "venv": true, "env": true, "__pycache__": true, "site-packages": true, "_build": true, "Pods": true, "DerivedData": true}
	goConfigs   = map[string]bool{"go.mod": true, "go.sum": true}
	dartConfigs = map[string]bool{"pubspec.yaml": true, "pubspec.lock": true, "analysis_options.yaml": true}
	rsConfigs   = map[string]bool{"Cargo.toml": true}
)

// scan lists the source files Codumi cares about, with size and mtime.
func scan(root string) map[string]store.FileState {
	out := map[string]store.FileState{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		lang := ""
		switch ext := filepath.Ext(name); {
		case tsExts[ext]:
			lang = "ts"
		case ext == ".go":
			lang = "go"
		case ext == ".dart":
			lang = "dart"
		case ext == ".py":
			lang = "py"
		case ext == ".kt" || ext == ".kts" || ext == ".java" || ext == ".scala":
			lang = "kt"
		case ext == ".rb" || ext == ".rake":
			lang = "rb"
		case ext == ".ex" || ext == ".exs":
			lang = "ex"
		case ext == ".swift":
			lang = "swift"
		case ext == ".prisma":
			lang = "orm-config"
		case ext == ".rs":
			lang = "rs"
		case ext == ".cs":
			lang = "cs"
		case ext == ".php" && !strings.HasSuffix(name, ".blade.php"):
			lang = "php"
		case ext == ".svelte" || ext == ".astro" || ext == ".vue":
			lang = "sfc"
		case rsConfigs[name]:
			lang = "rs-config"
		case dartConfigs[name]:
			lang = "dart-config"
		case configs[name]:
			lang = "ts-config"
		case goConfigs[name]:
			lang = "go-config"
		case ext == ".sql":
			lang = "sql"
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		out[rel] = store.FileState{Path: rel, Size: info.Size(), MTime: info.ModTime().UnixNano(), Lang: lang}
		return nil
	})
	return out
}

func stateList(m map[string]store.FileState) []store.FileState {
	out := make([]store.FileState, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func cmdIndex(pos []string, flags map[string]string) error {
	root := "."
	if len(pos) > 0 {
		root = pos[0]
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if flags["quiet"] == "true" {
		prevOut, prevNotes := out, notes
		out, notes = io.Discard, io.Discard
		defer func() { out, notes = prevOut, prevNotes }()
	}
	unlock, err := lockIndex(root)
	if err != nil {
		if flags["quiet"] == "true" {
			return nil
		}
		return err
	}
	defer unlock()
	start := time.Now()
	cur := scan(root)
	meta := map[string]string{"version": version, "schema": schemaVersion, "indexed_at": strconv.FormatInt(time.Now().Unix(), 10)}

	if flags["full"] != "true" {
		done, err := indexIncremental(root, cur, meta, start)
		if err != nil || done {
			return err
		}
	}
	return indexFull(root, cur, meta, start)
}

func indexFull(root string, cur map[string]store.FileState, meta map[string]string, start time.Time) error {
	g := &model.Graph{Root: root}
	hasTS := exists(filepath.Join(root, "tsconfig.json")) || exists(filepath.Join(root, "package.json")) || exists(filepath.Join(root, "jsconfig.json"))
	if hasTS {
		fmt.Fprintln(notes, "codumi: reading TypeScript with the project's compiler…")
		if err := extract.TypeScript(root, g, nil); err != nil {
			return err
		}
	}
	hasGo := len(extract.GoModules(root)) > 0
	if hasGo {
		fmt.Fprintln(notes, "codumi: reading Go with the type checker…")
		if err := extract.Go(root, g, nil); err != nil {
			return err
		}
	}
	hasDart := len(extract.DartProjects(root)) > 0
	if hasDart {
		fmt.Fprintln(notes, "codumi: reading Dart with package:analyzer…")
		if err := extract.Dart(root, g, nil); err != nil {
			return err
		}
	}
	hasPy := false
	for _, s := range cur {
		if s.Lang == "py" {
			hasPy = true
			break
		}
	}
	if hasPy {
		fmt.Fprintln(notes, "codumi: reading Python…")
		if err := extract.Python(root, g, nil); err != nil {
			return err
		}
	}
	hasSFC := false
	for _, s := range cur {
		hasSFC = hasSFC || s.Lang == "sfc"
	}
	if hasSFC {
		fmt.Fprintln(notes, "codumi: reading Vue/Svelte/Astro components…")
		if err := extract.SFC(root, g); err != nil {
			return err
		}
	}
	hasKt, hasRs, hasPHP, hasRb, hasEx, hasSwift := false, false, false, false, false, false
	for _, s := range cur {
		hasKt = hasKt || s.Lang == "kt" || s.Lang == "cs"
		hasRs = hasRs || s.Lang == "rs-config"
		hasPHP = hasPHP || s.Lang == "php"
		hasRb = hasRb || s.Lang == "rb"
		hasEx = hasEx || s.Lang == "ex"
		hasSwift = hasSwift || s.Lang == "swift"
	}
	if hasRb {
		fmt.Fprintln(notes, "codumi: reading Ruby…")
		if err := extract.Ruby(root, g, nil); err != nil {
			return err
		}
	}
	if hasEx {
		fmt.Fprintln(notes, "codumi: reading Elixir…")
		if err := extract.Elixir(root, g, nil); err != nil {
			return err
		}
	}
	if hasSwift {
		fmt.Fprintln(notes, "codumi: reading Swift…")
		if err := extract.Swift(root, g, nil); err != nil {
			return err
		}
	}
	if hasPHP {
		fmt.Fprintln(notes, "codumi: reading PHP…")
		if err := extract.PHP(root, g, nil); err != nil {
			return err
		}
	}
	if hasKt {
		fmt.Fprintln(notes, "codumi: reading Kotlin/Java/C#…")
		if err := extract.Kotlin(root, g, nil); err != nil {
			return err
		}
	}
	if hasRs {
		fmt.Fprintln(notes, "codumi: reading Rust…")
		if err := extract.Rust(root, g, nil); err != nil {
			return err
		}
	}
	extract.ORM(root, g, nil)
	migrations := sqlschema.FindMigrations(root)
	if len(migrations) > 0 {
		if err := sqlschema.Load(root, migrations, g); err != nil {
			return err
		}
	}
	if !hasRb && !hasEx && !hasSwift && !hasTS && !hasGo && !hasDart && !hasPy && !hasKt && !hasRs && !hasPHP && !hasSFC && len(migrations) == 0 {
		return errors.New("nothing to index: no tsconfig.json/package.json, go.mod, pubspec.yaml, Python, Kotlin/Java, C#, PHP, Cargo.toml, Ruby, Elixir, Swift, Vue/Svelte/Astro or SQL migrations found in " + root)
	}
	path, err := store.Save(root, g, meta, stateList(cur))
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(root, path)
	fmt.Fprintf(out, "indexed %d files: %d declarations, %d references, %d database uses, %d routes\n",
		len(g.Files), len(g.Decls), len(g.Refs), len(g.DBUses), len(g.Routes))
	if len(migrations) > 0 {
		fmt.Fprintf(out, "schema from %d migrations: %d tables/views, %d policies, %d functions, %d buckets\n",
			len(migrations), len(g.Tables), len(g.Policies), len(g.Functions), len(g.Buckets))
	}
	printWarnings(g)
	fmt.Fprintf(out, "done in %.1fs → %s\n", time.Since(start).Seconds(), rel)
	return nil
}

func printWarnings(g *model.Graph) {
	for _, w := range g.Warnings {
		if !strings.HasPrefix(w, "sql: ") {
			fmt.Fprintln(out, "note:", w)
		}
	}
}

// indexIncremental re-extracts only what changed since the last index. It
// returns done=false when a full index is needed instead.
func indexIncremental(root string, cur map[string]store.FileState, meta map[string]string, start time.Time) (bool, error) {
	path := filepath.Join(root, store.Dir, store.File)
	if !exists(path) {
		return false, nil
	}
	db, err := store.Open(path)
	if err != nil {
		return false, nil
	}
	var schema string
	db.QueryRow("SELECT value FROM meta WHERE key='schema'").Scan(&schema)
	old := store.States(db)
	if schema != schemaVersion || old == nil {
		db.Close()
		return false, nil
	}

	nuxt := exists(filepath.Join(root, "nuxt.config.ts")) || exists(filepath.Join(root, "nuxt.config.js")) || exists(filepath.Join(root, "nuxt.config.mjs"))
	changed := map[string][]string{} // lang → files
	gone := map[string][]string{}
	var goTouched []string
	for p, c := range cur {
		o, ok := old[p]
		if ok && o.Size == c.Size && o.MTime == c.MTime {
			continue
		}
		if nuxt && c.Lang == "ts" && strings.HasPrefix(p, "server/") {
			db.Close()
			return false, nil // rota do Nuxt: vem do caminho do arquivo
		}
		switch c.Lang {
		case "ts", "dart", "py", "kt", "rs", "cs", "php", "rb", "ex", "swift":
			changed[c.Lang] = append(changed[c.Lang], p)
		case "ts-config", "dart-config", "rs-config", "sfc", "orm-config":
			db.Close()
			return false, nil // compiler/package settings changed (or a Svelte/Astro file, read against the whole TS graph)
		case "go", "go-config":
			goTouched = append(goTouched, p)
		}
	}
	for p, o := range old {
		if _, ok := cur[p]; ok {
			continue
		}
		switch o.Lang {
		case "ts", "dart", "py", "kt", "rs", "cs", "php", "rb", "ex", "swift":
			gone[o.Lang] = append(gone[o.Lang], p)
		case "go", "go-config":
			goTouched = append(goTouched, p)
		case "ts-config", "dart-config", "rs-config", "sfc", "orm-config":
			db.Close()
			return false, nil
		}
	}

	g := &model.Graph{Root: root}
	replace := map[string]bool{}
	runners := map[string]func([]string) error{
		"ts":    func(only []string) error { return extract.TypeScript(root, g, only) },
		"dart":  func(only []string) error { return extract.Dart(root, g, only) },
		"py":    func(only []string) error { return extract.Python(root, g, only) },
		"kt":    func(only []string) error { return extract.Kotlin(root, g, only) },
		"rs":    func(only []string) error { return extract.Rust(root, g, only) },
		"cs":    func(only []string) error { return extract.Kotlin(root, g, only) },
		"php":   func(only []string) error { return extract.PHP(root, g, only) },
		"rb":    func(only []string) error { return extract.Ruby(root, g, only) },
		"ex":    func(only []string) error { return extract.Elixir(root, g, only) },
		"swift": func(only []string) error { return extract.Swift(root, g, only) },
	}
	nChanged := 0
	for _, lang := range []string{"ts", "dart", "py", "kt", "rs", "cs", "php", "rb", "ex", "swift"} {
		ch, gn := changed[lang], gone[lang]
		if len(ch)+len(gn) == 0 {
			continue
		}
		nChanged += len(ch) + len(gn)
		sort.Strings(ch)
		if err := refreshLang(db, g, replace, ch, gn, runners[lang]); err != nil {
			db.Close()
			return true, err
		}
	}

	// ORMs (Prisma, Drizzle, Django, SQLAlchemy) nos arquivos relidos
	var ormOnly []string
	for f := range replace {
		if l := cur[f].Lang; l == "ts" || l == "py" {
			ormOnly = append(ormOnly, f)
		}
	}
	if len(ormOnly) > 0 {
		sort.Strings(ormOnly)
		extract.ORM(root, g, ormOnly)
	}

	// Go: reload each touched module whole (go/packages is fast).
	var mods []string
	if len(goTouched) > 0 {
		all := extract.GoModules(root)
		seen := map[string]bool{}
		for _, f := range goTouched {
			abs := filepath.Join(root, filepath.FromSlash(f))
			best := ""
			for _, m := range all {
				if (strings.HasPrefix(abs, m+string(filepath.Separator)) || filepath.Dir(abs) == m) && len(m) > len(best) {
					best = m
				}
			}
			if best != "" && !seen[best] {
				seen[best] = true
				mods = append(mods, best)
			}
		}
		var rels []string
		for _, m := range all {
			r, _ := filepath.Rel(root, m)
			rels = append(rels, filepath.ToSlash(r))
		}
		reload := map[string]bool{}
		for _, m := range mods {
			r, _ := filepath.Rel(root, m)
			reload[filepath.ToSlash(r)] = true
		}
		mark := func(p string) {
			if reload[ownerModule(p, rels)] {
				replace[p] = true
			}
		}
		for p, s := range cur {
			if s.Lang == "go" {
				mark(p)
			}
		}
		for p, s := range old {
			if s.Lang == "go" {
				mark(p)
			}
		}
		if len(mods) > 0 {
			if err := extract.Go(root, g, mods); err != nil {
				db.Close()
				return true, err
			}
		}
	}
	db.Close()

	// SQL: replaying migrations takes milliseconds, so it always runs.
	migrations := sqlschema.FindMigrations(root)
	if len(migrations) > 0 {
		if err := sqlschema.Load(root, migrations, g); err != nil {
			return true, err
		}
	}

	files := make([]string, 0, len(replace))
	for f := range replace {
		files = append(files, f)
	}
	if err := store.Update(path, g, files, nil, meta, stateList(cur)); err != nil {
		return true, err
	}
	switch {
	case len(files) == 0:
		fmt.Fprintf(out, "up to date: no code changes since the last index (schema refreshed) in %.1fs\n", time.Since(start).Seconds())
	default:
		fmt.Fprintf(out, "updated %d %s", len(files), plural(len(files), "file", "files"))
		var parts []string
		if nChanged > 0 {
			parts = append(parts, fmt.Sprintf("%d changed", nChanged))
		}
		if len(files) > nChanged && len(mods) == 0 {
			parts = append(parts, "plus the files that used what was renamed or removed")
		}
		if len(mods) > 0 {
			parts = append(parts, fmt.Sprintf("%d Go %s reloaded", len(mods), plural(len(mods), "module", "modules")))
		}
		if len(parts) > 0 {
			fmt.Fprintf(out, " (%s)", strings.Join(parts, ", "))
		}
		fmt.Fprintf(out, " in %.1fs\n", time.Since(start).Seconds())
	}
	printWarnings(g)
	return true, nil
}

// ownerModule is the innermost module (relative dir) that contains p.
func ownerModule(p string, rels []string) string {
	best, bestLen := "", -1
	for _, m := range rels {
		in := m == "." || strings.HasPrefix(p, m+"/")
		l := len(m)
		if m == "." {
			l = 0
		}
		if in && l > bestLen {
			best, bestLen = m, l
		}
	}
	return best
}

// refreshLang re-walks the changed files of one language. Files that used a
// declaration that was renamed or removed are re-read too, so no reference
// points at a ghost.
func refreshLang(db *sql.DB, g *model.Graph, replace map[string]bool, changed, gone []string, run func([]string) error) error {
	before := map[[2]string]bool{}
	for _, f := range append(append([]string{}, changed...), gone...) {
		replace[f] = true
		rows, err := db.Query("SELECT name FROM decls WHERE file=?", f)
		if err != nil {
			continue
		}
		for rows.Next() {
			var n string
			rows.Scan(&n)
			before[[2]string{f, n}] = true
		}
		rows.Close()
	}
	start := len(g.Decls)
	if len(changed) > 0 {
		if err := run(changed); err != nil {
			return err
		}
	}
	after := map[[2]string]bool{}
	for _, d := range g.Decls[start:] {
		after[[2]string{d.File, d.Name}] = true
	}
	dependents := map[string]bool{}
	for k := range before {
		if after[k] {
			continue
		}
		rows, err := db.Query("SELECT DISTINCT file FROM refs WHERE to_file=? AND to_name=?", k[0], k[1])
		if err != nil {
			continue
		}
		for rows.Next() {
			var f string
			rows.Scan(&f)
			if !replace[f] {
				dependents[f] = true
			}
		}
		rows.Close()
	}
	if len(dependents) == 0 {
		return nil
	}
	var more []string
	for f := range dependents {
		more = append(more, f)
		replace[f] = true
	}
	sort.Strings(more)
	return run(more)
}
