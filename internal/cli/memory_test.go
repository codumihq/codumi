package cli

import (
	"strings"
	"testing"
)

func TestRemember(t *testing.T) {
	root := t.TempDir()
	if _, err := Remember(root, "Use sempre pnpm, nunca npm", ""); err != nil {
		t.Fatal(err)
	}
	Remember(root, "Migrations só pelo supabase db push", "")
	if msg, _ := Remember(root, "use sempre  pnpm, nunca npm", ""); !strings.HasPrefix(msg, "already") {
		t.Fatalf("duplicado: %q", msg)
	}
	if got := MemoryRead(root); len(got) != 2 || got[0].Date == "" {
		t.Fatalf("memória: %+v", got)
	}
	if !strings.Contains(MemoryPrompt(root), "- Migrations só pelo supabase db push") {
		t.Fatal(MemoryPrompt(root))
	}
	Remember(root, "", "pnpm")
	if got := MemoryRead(root); len(got) != 1 {
		t.Fatalf("forget: %+v", got)
	}
	if _, err := Remember(root, "", ""); err == nil {
		t.Fatal("sem nada devia dar erro")
	}
}
