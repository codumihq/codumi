package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Uma cópia do demo em Go num repositório git: mexer em markPaid tem de
// chegar nos dois webhooks que a chamam e na tabela que ela atualiza; uma
// função nova sem uso tem de aparecer em unused.
func TestImpactAndUnused(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	src, _ := filepath.Abs("../../examples/demo-go")
	dir := t.TempDir()
	for _, f := range []string{"go.mod", "main.go"} {
		b, _ := os.ReadFile(filepath.Join(src, f))
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-qm", "init")
	main := filepath.Join(dir, "main.go")
	b, _ := os.ReadFile(main)
	s := strings.Replace(string(b), "db.Exec(`INSERT INTO payment_events", "// registra o evento\n\tdb.Exec(`INSERT INTO payment_events", 1)
	s += "\nfunc semUso() int { return 1 }\n"
	os.WriteFile(main, []byte(s), 0o644)

	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	os.Chdir(dir)
	var buf bytes.Buffer
	prevOut, prevNotes := out, notes
	out, notes = &buf, &bytes.Buffer{}
	defer func() { out, notes = prevOut, prevNotes }()
	if err := cmdImpact(nil, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, w := range []string{"main.go: markPaid, semUso", "POST /v1/webhooks/pix", "POST /v1/webhooks/signed", "table orders", "update", "table payment_events"} {
		if !strings.Contains(got, w) {
			t.Errorf("impact: faltou %q\n%s", w, got)
		}
	}
	buf.Reset()
	if err := cmdUnused(nil, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if u := buf.String(); !strings.Contains(u, "semUso") || strings.Contains(u, "markPaid") {
		t.Errorf("unused:\n%s", u)
	}
}
