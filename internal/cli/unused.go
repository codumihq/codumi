package cli

// codumi unused: declarações que ninguém usa. Conta só usos de verdade
// (import e export não contam), e não lista o que o framework chama sozinho:
// telas e rotas, main/init, handlers HTTP (GET, POST…), testes, métodos
// (podem ser chamados por interface ou pelo framework) e arquivos de config.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var entryNames = map[string]bool{
	"main": true, "init": true, "default": true, "middleware": true, "proxy": true, "config": true,
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true,
	"generateMetadata": true, "generateStaticParams": true, "generateViewport": true, "metadata": true, "viewport": true,
	"revalidate": true, "dynamic": true, "runtime": true, "dynamicParams": true, "fetchCache": true, "preferredRegion": true, "maxDuration": true,
	"loader": true, "action": true, "meta": true, "links": true, "headers": true, "handle": true, "load": true, "prerender": true,
	"getServerSideProps": true, "getStaticProps": true, "getStaticPaths": true, "Route": true, "routeTree": true,
	"setUp": true, "tearDown": true, "setup": true, "teardown": true,
}

var testName = regexp.MustCompile(`^(Test|Benchmark|Example|Fuzz)[A-Z_]|^test_|^test[A-Z]`)

// entryFile: arquivos que o framework carrega pelo nome ou caminho.
func entryFile(f string) bool {
	b := filepath.Base(f)
	stem := strings.TrimSuffix(b, filepath.Ext(b))
	switch stem {
	case "page", "layout", "route", "loading", "error", "not-found", "template", "default", "global-error", "middleware", "proxy",
		"instrumentation", "opengraph-image", "twitter-image", "icon", "apple-icon", "sitemap", "robots", "manifest", "+page", "+layout", "+server", "+page.server", "+layout.server", "__root":
		return true
	}
	if strings.Contains(b, ".config.") || strings.HasSuffix(b, ".d.ts") || isTestFile(f) {
		return true
	}
	return strings.HasPrefix(f, "pages/") || strings.Contains(f, "/pages/") && !strings.Contains(f, "/components/") ||
		strings.Contains(f, "/routes/") || strings.HasPrefix(f, "src/routes/") || strings.HasSuffix(b, ".astro") || strings.HasSuffix(b, ".svelte")
}

func cmdUnused(pos []string, flags map[string]string) error {
	_, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	used := map[string]bool{} // arquivo|nome
	if rows, err := db.Query(`SELECT DISTINCT to_file, to_name FROM refs WHERE kind NOT IN ('import','export')`); err == nil {
		for rows.Next() {
			var f, n string
			rows.Scan(&f, &n)
			used[f+"|"+n] = true
			if i := strings.IndexByte(n, '.'); i > 0 { // usar um membro usa a classe
				used[f+"|"+n[:i]] = true
			}
		}
		rows.Close()
	}
	for _, q := range []string{`SELECT handler_file, handler FROM routes`, `SELECT file, component FROM pages`} {
		if rows, err := db.Query(q); err == nil {
			for rows.Next() {
				var f, n string
				rows.Scan(&f, &n)
				used[f+"|"+n] = true
			}
			rows.Close()
		}
	}
	importedOnly := map[string]bool{} // importado em algum lugar, mas nunca usado
	reExported := map[string]bool{}   // sai por export { X } ou export … from
	if rows, err := db.Query(`SELECT DISTINCT to_file, to_name, kind FROM refs WHERE kind IN ('import','export')`); err == nil {
		for rows.Next() {
			var f, n, k string
			rows.Scan(&f, &n, &k)
			if k == "import" {
				importedOnly[f+"|"+n] = true
			} else {
				reExported[f+"|"+n] = true
			}
		}
		rows.Close()
	}
	withMethods := flags["methods"] == "true"
	kindFilter := flags["kind"]
	type item struct {
		file, name, kind string
		line             int
		exported, imp    bool
	}
	var items []item
	rows, err := db.Query(`SELECT file, name, kind, line, exported FROM decls ORDER BY file, line`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var it item
		var ex int
		rows.Scan(&it.file, &it.name, &it.kind, &it.line, &ex)
		it.exported = ex == 1 || reExported[it.file+"|"+it.name]
		if used[it.file+"|"+it.name] || entryNames[it.name] || testName.MatchString(it.name) || entryFile(it.file) {
			continue
		}
		member := strings.Contains(it.name, ".")
		if member && !withMethods {
			continue
		}
		if strings.HasPrefix(it.name, "_") || strings.HasPrefix(it.name, "__") || it.kind == "module" || it.kind == "namespace" {
			continue
		}
		if kindFilter != "" && it.kind != kindFilter {
			continue
		}
		it.imp = importedOnly[it.file+"|"+it.name]
		items = append(items, it)
	}
	rows.Close()
	if len(items) == 0 {
		fmt.Fprintln(out, "no unused declarations found")
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].exported != items[j].exported {
			return !items[i].exported // internas primeiro: dá para apagar com mais segurança
		}
		return items[i].file < items[j].file
	})
	limit := intFlag(flags, "limit", 100)
	inner, exp := 0, 0
	for _, it := range items {
		if it.exported {
			exp++
		} else {
			inner++
		}
	}
	fmt.Fprintf(out, "%d declarations nobody uses: %d internal (safe to remove), %d exported (another project or a dynamic call may still use them)\n", len(items), inner, exp)
	cur := ""
	for i, it := range items {
		if limit > 0 && i >= limit {
			fmt.Fprintf(out, "… %d more (--limit=0 for all)\n", len(items)-limit)
			break
		}
		group := "internal"
		if it.exported {
			group = "exported"
		}
		if group != cur {
			cur = group
			fmt.Fprintf(out, "\n%s:\n", group)
		}
		note := ""
		if it.imp {
			note = "  (imported, never used)"
		}
		fmt.Fprintf(out, "  %s:%d  %s  [%s]%s\n", it.file, it.line, it.name, it.kind, note)
	}
	fmt.Fprintln(out, "\nMethods are left out (interfaces and frameworks call them); add --methods to include them.")
	return nil
}
