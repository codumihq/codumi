-- Demo schema with deliberate holes. Each one is caught by `codumi audit`.

create table public.orgs (
  id uuid primary key default gen_random_uuid(),
  name text not null
);
alter table public.orgs enable row level security;

create table public.org_members (
  org_id uuid references public.orgs(id),
  user_id uuid not null
);
alter table public.org_members enable row level security;

create table public.orders (
  id uuid primary key default gen_random_uuid(),
  org_id uuid references public.orgs(id),
  product_id uuid,
  quantity int,
  status text default 'open'
);
alter table public.orders enable row level security;

-- Hole 1: RLS never enabled.
create table public.audit_logs (
  id bigserial primary key,
  action text,
  order_id uuid
);

-- Caller-scoped helper: SECURITY DEFINER, but filters by auth.uid().
create or replace function public.my_org_ids() returns setof uuid
language sql stable security definer set search_path = public as $$
  select org_id from public.org_members where user_id = auth.uid();
$$;

create policy "members read orders" on public.orders
  for select to authenticated using (org_id in (select public.my_org_ids()));

create policy "members create orders" on public.orders
  for insert to authenticated with check (org_id in (select public.my_org_ids()));

-- Hole 2: SECURITY DEFINER, no caller check, executable by anon (the default).
create or replace function public.adjust_stock(p_product uuid, p_delta int) returns void
language plpgsql security definer set search_path = public as $$
begin
  update products set stock = stock + p_delta where id = p_product;
end;
$$;

-- Hole 3: a view runs as its owner, so it ignores the RLS on orders.
create view public.order_totals as
  select org_id, count(*) as orders from public.orders group by org_id;

-- Hole 4: public bucket whose SELECT policy only checks the bucket,
-- so anyone can LIST every file in it.
insert into storage.buckets (id, name, public) values ('attachments', 'attachments', true);
create policy "attachments are readable" on storage.objects
  for select using (bucket_id = 'attachments');

-- Public bucket without a listing policy: files readable by URL only.
insert into storage.buckets (id, name, public) values ('avatars', 'avatars', true);
