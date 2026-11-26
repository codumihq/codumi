-- Fixed correctly: SECURITY DEFINER, but EXECUTE revoked from anon.
-- codumi replays migrations in order, so this one is NOT reported.
create or replace function public.close_order(p_order uuid) returns void
language plpgsql security definer set search_path = public as $$
begin
  update orders set status = 'closed' where id = p_order;
end;
$$;

revoke execute on function public.close_order(uuid) from public, anon;
grant execute on function public.close_order(uuid) to authenticated;
