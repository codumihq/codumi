package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotesAPI(t *testing.T) {
	root := t.TempDir()
	if got := Slugify("Autenticação & Sessão"); got != "autenticacao-sessao" {
		t.Fatalf("slug: %q", got)
	}
	n, err := NewNote(root, "Pagamentos: Pix", "## Decisões\n- reconsultar no provedor")
	if err != nil || n.Slug != "pagamentos-pix" {
		t.Fatalf("new: %+v %v", n, err)
	}
	if again, _ := NewNote(root, "Pagamentos: Pix", ""); again.Slug != "pagamentos-pix-2" {
		t.Fatalf("unique slug: %q", again.Slug)
	}
	n.Description = `Cobranças "Pix" e cartão`
	n.Paths = []string{"src/lib/payments/**", "src/app/api/payments"}
	if _, err := WriteNote(root, n); err != nil {
		t.Fatal(err)
	}
	back, err := ReadNote(root, "pagamentos-pix")
	if err != nil || back.Name != "Pagamentos: Pix" || back.Description != "Cobranças 'Pix' e cartão" || len(back.Paths) != 2 || back.Body != "## Decisões\n- reconsultar no provedor" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if len(ListNotes(root)) != 2 {
		t.Fatal("list")
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".codumi", ".gitignore")); len(b) == 0 {
		t.Fatal("notes must stay tracked by git")
	}
	if _, err := ReadNote(root, "../etc/passwd"); err == nil {
		t.Fatal("slug must not escape the notes folder")
	}
	if err := DeleteNote(root, "pagamentos-pix-2"); err != nil || len(ListNotes(root)) != 1 {
		t.Fatal("delete")
	}
}
