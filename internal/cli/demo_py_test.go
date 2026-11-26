package cli

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codumihq/codumi/internal/audit"
	"github.com/codumihq/codumi/internal/extract"
	"github.com/codumihq/codumi/internal/model"
)

func TestDemoPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not in PATH")
	}
	root, _ := filepath.Abs("../../examples/demo-py")
	g := &model.Graph{Root: root}
	if err := extract.Python(root, g, nil); err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, r := range g.Refs {
		refs[r.From+" -> "+r.To+" ("+r.Kind+")"] = true
	}
	for _, want := range []string{
		"post_order -> create_order (call)",
		"pix_webhook -> OrderService.mark_paid (call)", // via svc = OrderService()
		"OrderService.mark_paid -> OrderService.notify (call)",
	} {
		if !refs[want] {
			t.Errorf("missing ref %q", want)
		}
	}
	var routes []string
	for _, r := range g.Routes {
		routes = append(routes, r.Method+" "+r.Path+" "+r.Handler)
	}
	sort.Strings(routes)
	if strings.Join(routes, "|") != "GET /health health|POST /v1/orders post_order|POST /v1/webhooks/pix pix_webhook" {
		t.Errorf("routes: %v", routes)
	}
	var db []string
	for _, u := range g.DBUses {
		db = append(db, u.Type+" "+u.Name+" "+strings.Join(u.Ops, ",")+" @"+u.From)
	}
	sort.Strings(db)
	if strings.Join(db, "|") != "rpc adjust_stock  @create_order|table orders insert @create_order|table orders update @OrderService.mark_paid" {
		t.Errorf("db: %v", db)
	}
	if len(g.RouteCalls) != 1 || g.RouteCalls[0].Path != "/v1/notifications/${}" || g.RouteCalls[0].Method != "POST" {
		t.Errorf("api calls: %+v", g.RouteCalls)
	}
	var got []string
	for _, f := range audit.Run(root, g) {
		got = append(got, f.Rule+" "+f.Target)
	}
	if strings.Join(got, "|") != "route-no-auth POST /v1/orders|webhook-unverified POST /v1/webhooks/pix" {
		t.Errorf("audit: %v", got)
	}
}
