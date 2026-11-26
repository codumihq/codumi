package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageAndFind(t *testing.T) {
	if _, err := os.Stat("../../examples/demo/node_modules/typescript"); err != nil && os.Getenv("CODUMI_TYPESCRIPT") == "" {
		t.Skip("typescript not installed in examples/demo")
	}
	t.Setenv("CODUMI_NO_USAGE", "1")
	root := filepath.Join(t.TempDir(), "demo")
	copyDir(t, "../../examples/demo", root)
	os.Symlink(filepath.Join(must(filepath.Abs("../../examples/demo")), "node_modules"), filepath.Join(root, "node_modules"))
	runIndex(t, root, true)

	o, n, code := Run(root, "page", "/")
	if code != 0 {
		t.Fatalf("page: %s %s", o, n)
	}
	for _, want := range []string{"page /  src/app/page.tsx", "Home", "├─ Hero", "└─ OrderButton  src/components/OrderButton.tsx", "ui: Button", "api calls: POST /api/orders"} {
		if !strings.Contains(o, strings.TrimPrefix(want, "├─ ")) {
			t.Errorf("page output misses %q:\n%s", want, o)
		}
	}
	if strings.Contains(o, "button.tsx") {
		t.Errorf("primitive listed in the tree:\n%s", o)
	}
	o, _, _ = Run(root, "page", "/", "--all")
	if !strings.Contains(o, "Button  src/components/ui/button.tsx") {
		t.Errorf("--all should expand primitives:\n%s", o)
	}

	o, _, _ = Run(root, "find", "fresh coffee")
	if !strings.Contains(o, "1 match in 1 file") || !strings.Contains(o, "Hero") {
		t.Errorf("find:\n%s", o)
	}
}
