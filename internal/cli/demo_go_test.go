package cli

import (
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/codumihq/codumi/internal/audit"
	"github.com/codumihq/codumi/internal/extract"
	"github.com/codumihq/codumi/internal/model"
)

func TestDemoGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}
	root, _ := filepath.Abs("../../examples/demo-go")
	g := &model.Graph{Root: root}
	if err := extract.Go(root, g, nil); err != nil {
		t.Fatal(err)
	}
	handlers := map[string]string{}
	for _, r := range g.Routes {
		handlers[r.Method+" "+r.Path] = r.Handler
	}
	want := map[string]string{
		"GET /v1/orders/{id}":      "Server.getOrder", // through the auth middleware
		"POST /v1/webhooks/pix":    "Server.pixWebhook",
		"POST /v1/webhooks/signed": "Server.signedWebhook",
	}
	for k, v := range want {
		if handlers[k] != v {
			t.Errorf("route %s: handler %q, want %q (all: %v)", k, handlers[k], v, handlers)
		}
	}
	var tables []string
	for _, u := range g.DBUses {
		tables = append(tables, u.Name+":"+u.Ops[0]+"@"+u.From)
	}
	sort.Strings(tables)
	wantT := []string{"orders:select@Server.getOrder", "orders:update@markPaid", "payment_events:insert@markPaid"}
	if len(tables) != len(wantT) {
		t.Fatalf("db uses %v, want %v", tables, wantT)
	}
	for i := range wantT {
		if tables[i] != wantT[i] {
			t.Fatalf("db uses %v, want %v", tables, wantT)
		}
	}
	calls := 0
	for _, r := range g.Refs {
		if r.To == "markPaid" && r.Kind == "call" {
			calls++
		}
	}
	if calls != 2 {
		t.Errorf("markPaid calls: %d, want 2", calls)
	}
	var got []string
	for _, f := range audit.Run(root, g) {
		got = append(got, f.Rule+" "+f.Target)
	}
	if len(got) != 1 || got[0] != "webhook-unverified POST /v1/webhooks/pix" {
		t.Errorf("audit: %v", got)
	}
	if !matchRoute("/v1/orders/{id}", "/v1/orders/${}") || !matchRoute("/files/{path...}", "/files/a/b") {
		t.Error("matchRoute go patterns")
	}
}
