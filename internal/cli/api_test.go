package cli

import (
	"strings"
	"testing"
)

func TestRunCapturesOutputAndErrors(t *testing.T) {
	o, n, code := Run(t.TempDir(), "refs", "x")
	if code != 1 || o != "" || !strings.Contains(n, "no index found") {
		t.Fatalf("code=%d out=%q notes=%q", code, o, n)
	}
}
