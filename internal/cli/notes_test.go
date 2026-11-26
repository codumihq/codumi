package cli

import "testing"

func TestGlobAndNote(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"src/lib/**", "src/lib/a/b.ts", true},
		{"src/lib/**/*.ts", "src/lib/x.ts", true},
		{"src/lib/*.ts", "src/lib/a/x.ts", false},
		{"src/app/api/orders", "src/app/api/orders/route.ts", true},
		{"src/app/api/orders", "src/app/api/orders-old/route.ts", false},
		{"src/lib/orders.ts", "src/lib/orders.ts", true},
		{"server/", "server/main.go", true},
	}
	for _, c := range cases {
		if got := globRe(c.glob).MatchString(c.path); got != c.want {
			t.Errorf("%s ~ %s = %v, want %v", c.glob, c.path, got, c.want)
		}
	}
	n, err := parseNote("../../examples/demo/.codumi/notes/orders.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "Orders" || len(n.Paths) != 2 || !n.matcher()("src/app/api/orders/route.ts") {
		t.Fatalf("note: %+v", n)
	}
}
