// Stand-ins for @supabase/supabase-js so the demo needs no install.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function createClient(): any {
  return {}
}

/** Service-role client: bypasses RLS. Server only. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function supabaseAdmin(): any {
  return {}
}
