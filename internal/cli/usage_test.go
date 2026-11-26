package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageLogAndStats(t *testing.T) {
	t.Setenv("CODUMI_USAGE_DB", filepath.Join(t.TempDir(), "usage.db"))
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "supabase", "migrations"), 0o755)
	os.WriteFile(filepath.Join(root, "supabase", "migrations", "001.sql"), []byte(strings.Repeat("create table public.t (id int);\n", 200)), 0o644)
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	os.Chdir(root)

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"codumi_audit","arguments":{}}}` + "\n")
	if err := serveMCP(in, io.Discard); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	out = &buf
	defer func() { out = os.Stdout }()
	if err := cmdStats(nil, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, "1 call from agents") || !strings.Contains(s, "audit") {
		t.Fatalf("stats:\n%s", s)
	}
}
