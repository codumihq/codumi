package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLook(t *testing.T) {
	if browserPath() == "" {
		t.Skip("no browser")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<h1 style="color:green">Olá</h1><script>console.error("quebrou aqui")</script>`))
	}))
	defer srv.Close()
	root := t.TempDir()
	os.MkdirAll(root+"/.codumi", 0o755)
	os.WriteFile(PreviewFile(root), []byte(`{"url":"`+srv.URL+`"}`), 0o644)
	text, img, err := Look(root, "", "/inicio", 390)
	if err != nil {
		t.Fatal(err)
	}
	if len(img) < 1000 || string(img[1:4]) != "PNG" {
		t.Fatal("sem imagem")
	}
	t.Log(text)
	if !strings.Contains(text, "/inicio") || !strings.Contains(text, "quebrou aqui") {
		t.Fatal(text)
	}
}
