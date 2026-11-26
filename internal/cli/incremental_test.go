package cli

import (
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/codumihq/codumi/internal/store"
)

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if d.Name() == ".codumi" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

func dump(t *testing.T, root string) map[string][]string {
	t.Helper()
	db, err := store.Open(filepath.Join(root, store.Dir, store.File))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string][]string{}
	for _, tbl := range []string{"files", "decls", "refs", "db_uses", "routes", "pages", "route_calls", "sql_tables", "sql_policies", "sql_functions", "buckets"} {
		rows, err := db.Query("SELECT * FROM " + tbl)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			var parts []string
			for _, v := range vals {
				parts = append(parts, v.String)
			}
			out[tbl] = append(out[tbl], strings.Join(parts, "|"))
		}
		rows.Close()
		sort.Strings(out[tbl])
	}
	return out
}

func runIndex(t *testing.T, root string, full bool) {
	t.Helper()
	prev := out
	out = io.Discard
	defer func() { out = prev }()
	f := map[string]string{}
	if full {
		f["full"] = "true"
	}
	if err := cmdIndex([]string{root}, f); err != nil {
		t.Fatal(err)
	}
}

// TestIncrementalMatchesFull edits, adds, deletes and renames code, then
// checks that an incremental index equals a fresh full one, row for row.
func TestIncrementalMatchesFull(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if os.Getenv("CODUMI_TYPESCRIPT") == "" {
		if _, err := os.Stat("../../examples/demo/node_modules/typescript"); err != nil {
			t.Skip("typescript not installed in examples/demo")
		}
	}
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	copyDir(t, "../../examples/demo", app)
	copyDir(t, "../../examples/demo-go", filepath.Join(app, "api"))
	if _, err := os.Stat(filepath.Join(app, "node_modules")); err != nil {
		os.Symlink(filepath.Join(must(filepath.Abs("../../examples/demo")), "node_modules"), filepath.Join(app, "node_modules"))
	}
	runIndex(t, app, true)

	write := func(rel, content string) {
		p := filepath.Join(app, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		future := time.Now().Add(2 * time.Second)
		os.Chtimes(p, future, future)
	}
	read := func(rel string) string { b, _ := os.ReadFile(filepath.Join(app, rel)); return string(b) }

	// rename an exported function used by two other files
	write("src/lib/orders.ts", strings.ReplaceAll(read("src/lib/orders.ts"), "export async function markPaid", "export async function settleOrder"))
	// a new file that calls something existing
	write("src/lib/report.ts", "import { createOrder } from '@/lib/orders'\n\nexport async function reorder() {\n  return createOrder({ productId: 'x', quantity: 1 })\n}\n")
	// delete a file
	os.Remove(filepath.Join(app, "src/components/OrderButton.tsx"))
	// touch Go
	write("api/main.go", strings.Replace(read("api/main.go"), "func markPaid(", "func markPaidNow(", 1))

	runIndex(t, app, false)
	inc := dump(t, app)

	fresh := filepath.Join(tmp, "fresh")
	copyDir(t, app, fresh)
	os.Remove(filepath.Join(fresh, "node_modules"))
	os.Symlink(filepath.Join(must(filepath.Abs("../../examples/demo")), "node_modules"), filepath.Join(fresh, "node_modules"))
	runIndex(t, fresh, true)
	full := dump(t, fresh)

	for tbl := range full {
		if !reflect.DeepEqual(inc[tbl], full[tbl]) {
			t.Errorf("%s differs after incremental index:\n incremental %d rows: %q\n full        %d rows: %q", tbl, len(inc[tbl]), inc[tbl], len(full[tbl]), full[tbl])
		}
	}

	// A second run with nothing changed is a no-op.
	runIndex(t, app, false)
	if again := dump(t, app); !reflect.DeepEqual(again, inc) {
		t.Error("no-change incremental run modified the index")
	}
}

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}
