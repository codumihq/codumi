package cli

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Um PR que cria o webhook de pagamentos sem verificar a assinatura: o
// comentário mostra a rota nova, o markPaid que ela chama e o achado da
// auditoria só nesse arquivo; os achados antigos ficam como "já existiam".
func TestPRComment(t *testing.T) {
	src, _ := filepath.Abs("../../examples/demo")
	ts := filepath.Join(src, "node_modules", "typescript")
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := os.Stat(ts); err != nil {
		t.Skip("typescript not installed in examples/demo")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	t.Setenv("CODUMI_TYPESCRIPT", ts)
	dir := t.TempDir()
	filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".codumi") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
			os.WriteFile(filepath.Join(dir, rel), b, 0o644)
		}
		return nil
	})
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	hook := filepath.Join(dir, "src/app/api/webhooks/payments/route.ts")
	hb, _ := os.ReadFile(hook)
	os.Remove(hook)
	git("init", "-q")
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".codumi/\n"), 0o644)
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	os.WriteFile(hook, hb, 0o644)
	git("add", ".")
	git("commit", "-qm", "webhook")

	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	os.Chdir(dir)
	var buf bytes.Buffer
	prevOut, prevNotes := out, notes
	out, notes = &buf, &bytes.Buffer{}
	defer func() { out, notes = prevOut, prevNotes }()
	err := cmdPR(nil, map[string]string{"base": base, "lang": "pt", "fail-on": "medium"})
	got := buf.String()
	if err != errPRFail {
		t.Errorf("fail-on medium should fail on the unverified webhook, got %v", err)
	}
	for _, w := range []string{prMarker, "o que este PR alcança", "src/app/api/webhooks/payments/route.ts", "webhook-unverified", "POST /api/webhooks/payments", "outros achados já existiam"} {
		if !strings.Contains(got, w) {
			t.Errorf("faltou %q\n%s", w, got)
		}
	}
	if strings.Contains(got, "rls-disabled") {
		t.Errorf("finding from an unchanged file in the PR section\n%s", got)
	}
}
