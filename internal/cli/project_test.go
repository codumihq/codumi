package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectArgument(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CODUMI_CONFIG_DIR", cfg)
	t.Setenv("CODUMI_NO_USAGE", "1")
	proj := filepath.Join(t.TempDir(), "shop")
	os.MkdirAll(filepath.Join(proj, "supabase", "migrations"), 0o755)
	os.WriteFile(filepath.Join(proj, "supabase", "migrations", "001.sql"), []byte("create table public.orders (id int);\n"), 0o644)
	b, _ := json.Marshal([]string{proj})
	os.WriteFile(filepath.Join(cfg, "projects.json"), b, 0o644)

	elsewhere := t.TempDir() // like a session opened in the home folder
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	os.Chdir(elsewhere)

	tl := tools()
	var table tool
	for _, x := range tl {
		if x.Name == "codumi_table" {
			table = x
		}
	}
	// Without project, from a folder that is not a project: explain, don't index it.
	text, isErr := runTool(table, map[string]any{"name": "orders"})
	if !isErr || !strings.Contains(text, "Pass `project`") {
		t.Fatalf("want a hint, got %q", text)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, ".codumi")); err == nil {
		t.Fatal("indexed a folder that is not a project")
	}
	// With the project's name: indexes it on first use and answers.
	text, isErr = runTool(table, map[string]any{"name": "orders", "project": "Shop"})
	if isErr || !strings.Contains(text, "table public.orders") {
		t.Fatalf("by name: %q", text)
	}
	if cwd, _ := os.Getwd(); filepath.Base(cwd) != filepath.Base(elsewhere) {
		t.Fatalf("did not return to the session folder: %s", cwd)
	}
	if text, isErr = runTool(table, map[string]any{"name": "orders", "project": "nope"}); !isErr || !strings.Contains(text, "Known projects: shop") {
		t.Fatalf("unknown project: %q", text)
	}
}
