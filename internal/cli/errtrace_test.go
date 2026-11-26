package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseFrames(t *testing.T) {
	cases := map[string][2]string{
		"node":   {"TypeError: Cannot read properties of undefined (reading 'id')\n    at markPaid (/var/task/src/lib/orders.ts:17:30)\n    at async POST (webpack-internal:///./src/app/api/webhooks/payments/route.ts:7:5)\n    at node:internal/process/task_queues:95:5", "/var/task/src/lib/orders.ts:17 markPaid|./src/app/api/webhooks/payments/route.ts:7 POST"},
		"python": {"Traceback (most recent call last):\n  File \"/app/app/views.py\", line 42, in create_order\n    order.save()\nValueError: bad", "/app/app/views.py:42 create_order"},
		"go":     {"panic: runtime error: invalid memory address\n\ngoroutine 1 [running]:\nmain.markPaid(...)\n\t/src/main.go:59 +0x1d\nmain.(*server).pixWebhook()\n\t/src/main.go:88 +0x45", "/src/main.go:59 |/src/main.go:88 "},
		"kotlin": {"java.lang.IllegalStateException: x\n\tat com.app.OrderService.pay(OrderService.kt:31)", "OrderService.kt:31 pay"},
		"ruby":   {"NoMethodError: undefined method\napp/models/order.rb:12:in `pay'", "app/models/order.rb:12 "},
		"php":    {"Error: x\n#0 /var/www/app/Http/OrderController.php(27): pay()", "/var/www/app/Http/OrderController.php:27 pay"},
		"dart":   {"Null check operator\n#0      OrderPage.build (package:loja/pages/order.dart:40:12)", "pages/order.dart:40 OrderPage.build"},
	}
	for name, c := range cases {
		_, fr := parseFrames(c[0])
		var got []string
		for _, f := range fr {
			got = append(got, f.Path+":"+itoa(f.Line)+" "+f.Fn)
		}
		if strings.Join(got, "|") != c[1] {
			t.Errorf("%s:\n got  %s\n want %s", name, strings.Join(got, "|"), c[1])
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Um panic no markPaid do demo em Go: a pilha chega nos dois webhooks que o
// chamam e na tabela orders que ele atualiza.
func TestErrorTrace(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}
	src, _ := filepath.Abs("../../examples/demo-go")
	dir := t.TempDir()
	for _, f := range []string{"go.mod", "main.go"} {
		b, _ := os.ReadFile(filepath.Join(src, f))
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	os.Chdir(dir)
	var buf bytes.Buffer
	prevOut, prevNotes := out, notes
	out, notes = &buf, &bytes.Buffer{}
	defer func() { out, notes = prevOut, prevNotes }()
	trace := "panic: runtime error: invalid memory address or nil pointer dereference\n\ngoroutine 7 [running]:\nmain.markPaid(0x0, {0x1, 0x2})\n\t/build/app/main.go:59 +0x1d\nnet/http.HandlerFunc.ServeHTTP()\n\t/usr/local/go/src/net/http/server.go:2220 +0x29\n"
	if err := cmdError(nil, map[string]string{"text": trace}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, w := range []string{"error: panic: runtime error", "main.go:59  in markPaid", "POST /v1/webhooks/pix", "POST /v1/webhooks/signed", "table orders", "1 lines outside the project"} {
		if !strings.Contains(got, w) {
			t.Errorf("faltou %q\n%s", w, got)
		}
	}
}
