package sqlschema

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestSplitKeepsBodiesAndLines(t *testing.T) {
	src := "-- comment; not a statement\ncreate table a (id int);\n\ncreate function f() returns void language plpgsql as $fn$\nbegin\n  perform 1; -- inside body\nend;\n$fn$;\nselect ';' as semi;"
	st := Split(src)
	if len(st) != 3 {
		t.Fatalf("want 3 statements, got %d: %#v", len(st), st)
	}
	if st[1].Line != 4 || len(st[1].Bodies) != 1 {
		t.Fatalf("function statement: line %d, %d bodies", st[1].Line, len(st[1].Bodies))
	}
	if st[1].Skel != "create function f() returns void language plpgsql as $BODY$" {
		t.Fatalf("skeleton: %q", st[1].Skel)
	}
}

func load(t *testing.T, sql string) *model.Graph {
	t.Helper()
	p := newParser()
	for _, st := range Split(sql) {
		p.exec(norm(st.Skel), st.Bodies, "test.sql")
	}
	g := &model.Graph{}
	p.finish(g)
	return g
}

func TestReplay(t *testing.T) {
	g := load(t, `
create table public.t (id int);
alter table public.t enable row level security;
create policy "p" on public.t for select to anon using (true);
drop policy if exists "p" on public.t;
create policy "q" on public.t as permissive for update to authenticated using (auth.uid() is not null) with check (true);
alter table public.t rename to t2;

create or replace function public.f(a int) returns int language sql security definer as $$ select a $$;
revoke execute on function public.f(int) from public, anon;
create or replace function public.f(a int) returns int language sql security definer as $$ select a + 1 $$;

create function public.g() returns trigger language plpgsql security definer as $$ begin return new; end $$;
do $$ begin
  if not exists (select 1 from pg_policies where policyname = 'r') then
    create policy "r" on public.t2 for delete using (false);
  end if;
end $$;
insert into storage.buckets (id, name, public) values ('b1', 'b1', false), ('b2', 'b2', true) on conflict (id) do nothing;
update storage.buckets set public = true where id = 'b1';
`)
	if len(g.Tables) != 1 || g.Tables[0].Name != "t2" || !g.Tables[0].RLS {
		t.Fatalf("tables: %+v", g.Tables)
	}
	if len(g.Policies) != 2 {
		t.Fatalf("want policies q and r (renamed table), got %+v", g.Policies)
	}
	for _, p := range g.Policies {
		if p.Table != "t2" {
			t.Fatalf("policy %q kept old table %q", p.Name, p.Table)
		}
		if p.Name == "q" && (p.Cmd != "update" || p.Check != "(true)" || p.Roles[0] != "authenticated") {
			t.Fatalf("policy q: %+v", p)
		}
	}
	var f model.Function
	for _, x := range g.Functions {
		if x.Name == "f" {
			f = x
		}
	}
	for _, r := range f.Exec {
		if r == "anon" || r == "public" {
			t.Fatalf("create or replace must keep the revoke; exec = %v", f.Exec)
		}
	}
	if len(g.Buckets) != 2 || !g.Buckets[0].Public || !g.Buckets[1].Public {
		t.Fatalf("buckets: %+v", g.Buckets)
	}
}

func TestFunctionArgsAndDatabaseUses(t *testing.T) {
	g := load(t, `
create table public.sales (id int, org uuid);
create or replace function public.adjust_stock(p_product uuid, p_delta numeric default 1) returns void
language plpgsql security definer as $$ begin update products set stock = stock + p_delta where id = p_product; end $$;
create function public.tg_sale() returns trigger language plpgsql as $$ begin perform adjust_stock (new.id, -1); return new; end $$;
create trigger trg_sale after insert on sales for each row execute function public.tg_sale();
create trigger trg_old after insert on public.sales for each row execute procedure tg_sale();
drop trigger if exists trg_old on public.sales;
create function public.my_orgs() returns setof uuid language sql as $$ select org from sales $$;
create policy "members" on public.sales for select using (org in (select my_orgs()));
`)
	for _, f := range g.Functions {
		if f.Name == "adjust_stock" && f.Args != "p_product uuid, p_delta numeric default 1" {
			t.Fatalf("args: %q", f.Args)
		}
	}
	var got []string
	for _, r := range g.SQLRefs {
		got = append(got, r.Kind+" "+r.Name+" -> "+r.To)
	}
	want := []string{
		"function public.tg_sale() -> adjust_stock",
		`policy "members" on public.sales -> my_orgs`,
		"trigger trg_sale on public.sales -> tg_sale",
	}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q\nwant %q", got, want)
		}
	}
}
