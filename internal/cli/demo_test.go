package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/codumihq/codumi/internal/audit"
	"github.com/codumihq/codumi/internal/extract"
	"github.com/codumihq/codumi/internal/model"
	"github.com/codumihq/codumi/internal/sqlschema"
)

// TestDemo runs the whole pipeline on examples/demo. It needs node and the
// typescript package (npm i in examples/demo, or CODUMI_TYPESCRIPT).
func TestDemo(t *testing.T) {
	root, _ := filepath.Abs("../../examples/demo")
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if os.Getenv("CODUMI_TYPESCRIPT") == "" {
		if _, err := os.Stat(filepath.Join(root, "node_modules", "typescript")); err != nil {
			t.Skip("typescript not installed in examples/demo (npm i) and CODUMI_TYPESCRIPT not set")
		}
	}
	g := &model.Graph{Root: root}
	if err := extract.TypeScript(root, g, nil); err != nil {
		t.Fatal(err)
	}
	if err := sqlschema.Load(root, sqlschema.FindMigrations(root), g); err != nil {
		t.Fatal(err)
	}

	calls := 0
	for _, r := range g.Refs {
		if r.To == "markPaid" && r.Kind == "call" {
			calls++
		}
	}
	if calls != 2 {
		t.Errorf("markPaid: want 2 exact calls, got %d", calls)
	}

	var got []string
	for _, f := range audit.Run(root, g) {
		got = append(got, f.Level+" "+f.Rule+" "+f.Target)
	}
	sort.Strings(got)
	want := []string{
		"HIGH rls-disabled public.audit_logs",
		"HIGH security-definer-anon public.adjust_stock()",
		"HIGH storage-listable bucket attachments",
		"LOW bucket-public bucket avatars",
		"LOW security-definer-anon public.my_org_ids()",
		"MEDIUM view-security-definer public.order_totals",
		"MEDIUM webhook-unverified POST /api/webhooks/payments",
	}
	if len(got) != len(want) {
		t.Fatalf("findings:\n got %q\nwant %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("findings:\n got %q\nwant %q", got, want)
		}
	}
	if !matchRoute("/api/orders/[id]", "/api/orders/${}") || matchRoute("/api/orders", "/api/orders/x") {
		t.Error("matchRoute")
	}
}
