package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// startPG sobe um Postgres descartável (o do Homebrew, ou CODUMI_PG_BIN).
func startPG(t *testing.T) string {
	bin := os.Getenv("CODUMI_PG_BIN")
	if bin == "" {
		bin = "/opt/homebrew/opt/postgresql@17/bin"
	}
	if _, err := os.Stat(filepath.Join(bin, "initdb")); err != nil {
		t.Skip("no Postgres to test with (brew install postgresql@17 or CODUMI_PG_BIN)")
	}
	data := t.TempDir()
	if out, err := exec.Command(filepath.Join(bin, "initdb"), "-D", data, "-U", "postgres", "--auth=trust", "-E", "UTF8").CombinedOutput(); err != nil {
		t.Fatalf("initdb: %s", out)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cmd := exec.Command(filepath.Join(bin, "postgres"), "-D", data, "-p", fmt.Sprint(port), "-c", "listen_addresses=127.0.0.1", "-c", "unix_socket_directories=")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Signal(os.Interrupt); cmd.Wait() })
	url := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
	for i := 0; i < 50; i++ {
		if c, err := pgx.Connect(context.Background(), url); err == nil {
			c.Close(context.Background())
			return url
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("postgres did not start")
	return ""
}

func TestDBDrift(t *testing.T) {
	url := startPG(t)
	ctx := context.Background()
	c, _ := pgx.Connect(ctx, url)
	// o banco "de verdade", com o que foi mexido no painel
	for _, q := range []string{
		`CREATE ROLE anon NOLOGIN`, `CREATE ROLE authenticated NOLOGIN`,
		`CREATE TABLE public.orders (id int, user_id text)`, `ALTER TABLE public.orders ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY own ON public.orders FOR SELECT TO authenticated USING (user_id = current_setting('request.jwt.claims', true)::json->>'sub')`,
		`INSERT INTO public.orders VALUES (1, 'someone-else')`,
		`CREATE TABLE public.profiles (id int, name text)`, // RLS desligado no painel
		`INSERT INTO public.profiles VALUES (1, 'Ana')`,
		`CREATE TABLE public.leads (email text)`, `ALTER TABLE public.leads ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY anyone ON public.leads FOR INSERT TO anon WITH CHECK (true)`,
		`CREATE TABLE public.painel (x int)`, // criada no painel
		`CREATE FUNCTION public.secret() RETURNS int LANGUAGE sql AS 'select 1'`,
		`GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA public TO anon, authenticated`,
	} {
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	c.Close(ctx)

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "supabase", "migrations"), 0o755)
	os.WriteFile(filepath.Join(dir, "supabase", "migrations", "001_init.sql"), []byte(`
create table public.orders (id int, user_id text);
alter table public.orders enable row level security;
create policy own on public.orders for select to authenticated using (user_id = auth.uid()::text);
create table public.profiles (id int, name text);
alter table public.profiles enable row level security;
create table public.leads (email text);
alter table public.leads enable row level security;
create policy anyone on public.leads for insert to anon with check (true);
create table public.pending_feature (id int);
create function public.secret() returns int language sql security definer as 'select 1';
`), 0o644)
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	os.Chdir(dir)
	var buf bytes.Buffer
	prevOut, prevNotes := out, notes
	out, notes = &buf, &bytes.Buffer{}
	defer func() { out, notes = prevOut, prevNotes }()
	if err := cmdDB(nil, map[string]string{"db": url, "rls": "true"}); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	got := buf.String()
	t.Log(got)
	for _, w := range []string{
		"public.painel", "in no migration",
		"public.pending_feature", "not in the database",
		"public.profiles", "RLS is off in the database and on in the migrations",
		"public.secret", "SECURITY DEFINER is off in the database and on in the migrations",
		"anon           public.profiles", "READS rows",
		"public.leads", "can INSERT (open policy)", "can INSERT (no RLS)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("faltou %q", w)
		}
	}
	// orders: a política só deixa o dono ler; ninguém de fora lê
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "public.orders") && strings.Contains(l, "READS") {
			t.Errorf("orders should not be readable: %s", l)
		}
	}
	// e nada foi gravado: a transação era só leitura
	c, _ = pgx.Connect(ctx, url)
	defer c.Close(ctx)
	var n int
	c.QueryRow(ctx, "SELECT count(*) FROM public.leads").Scan(&n)
	if n != 0 {
		t.Fatal("the check wrote to the database")
	}
}
