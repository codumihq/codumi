package cli

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codumihq/codumi/internal/extract"
	"github.com/codumihq/codumi/internal/model"
)

func TestDemoDart(t *testing.T) {
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skip("dart not in PATH")
	}
	root, _ := filepath.Abs("../../examples/demo-dart")
	g := &model.Graph{Root: root}
	if err := extract.Dart(root, g, nil); err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, r := range g.Refs {
		if r.To == "OrderService.createOrder" && r.Kind == "call" && r.File == "lib/main.dart" {
			calls++
		}
	}
	if calls != 2 {
		t.Errorf("OrderService.createOrder: %d calls from main, want 2", calls)
	}
	var db []string
	for _, u := range g.DBUses {
		db = append(db, u.Type+" "+u.Name+" "+strings.Join(u.Ops, ",")+" @"+u.From)
	}
	sort.Strings(db)
	want := []string{
		"bucket receipts upload @OrderService.attachReceipt",
		"rpc adjust_stock  @OrderService.createOrder",
		"table orders insert,select @OrderService.createOrder",
	}
	if strings.Join(db, "\n") != strings.Join(want, "\n") {
		t.Errorf("db uses:\n%s\nwant:\n%s", strings.Join(db, "\n"), strings.Join(want, "\n"))
	}
	var calls2 []string
	for _, c := range g.RouteCalls {
		calls2 = append(calls2, c.Method+" "+c.Path)
	}
	sort.Strings(calls2)
	if strings.Join(calls2, ",") != "GET /v1/orders/${},POST /v1/orders" {
		t.Errorf("api calls: %v", calls2)
	}
}
