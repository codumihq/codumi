package cli

// codumi banco: compara o banco de verdade com as migrações do projeto (o que
// foi mudado direto no painel e nunca virou migração, e o que está nas
// migrações mas não no banco) e, com --rls, testa o que anon e um usuário
// logado qualquer conseguem ler. Tudo numa transação SÓ LEITURA, com limite de
// 3 s por consulta e sem contar linhas (só "existe alguma?").

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// esquemas do próprio Supabase/Postgres: ficam de fora
var dbSkipSchemas = map[string]bool{
	"auth": true, "storage": true, "realtime": true, "extensions": true, "graphql": true, "graphql_public": true,
	"pgsodium": true, "pgsodium_masks": true, "vault": true, "net": true, "cron": true, "pgbouncer": true,
	"supabase_functions": true, "supabase_migrations": true, "_realtime": true, "_analytics": true, "information_schema": true,
}

type dbItem struct {
	Kind   string `json:"kind"`   // table, rls, policy, function, secdef
	Name   string `json:"name"`   // schema.tabela, política em schema.tabela…
	Where  string `json:"where"`  // db (só no banco), migrations (só nas migrações), both (diferente)
	Detail string `json:"detail"` // o que difere
	Src    string `json:"src,omitempty"`
}

type rlsProbe struct {
	Table  string `json:"table"`
	Role   string `json:"role"`
	Reads  bool   `json:"reads"`            // a consulta devolveu alguma linha
	Writes string `json:"writes,omitempty"` // INSERT/UPDATE/DELETE liberados
	Why    string `json:"why,omitempty"`    // "no RLS" ou "open policy"
	Err    string `json:"err,omitempty"`
}

type dbReport struct {
	Tables  int        `json:"tables"`
	Drift   []dbItem   `json:"drift"`
	RLS     []rlsProbe `json:"rls,omitempty"`
	Skipped string     `json:"skipped,omitempty"`
}

func dbURL(flags map[string]string) string {
	for _, v := range []string{flags["db"], os.Getenv("CODUMI_DB_URL"), os.Getenv("SUPABASE_DB_URL"), os.Getenv("DATABASE_URL")} {
		if v != "" {
			return v
		}
	}
	return ""
}

func cmdDB(pos []string, flags map[string]string) error {
	url := dbURL(flags)
	if url == "" {
		return errors.New("pass the database connection (--db=postgres://… or CODUMI_DB_URL). Read-only queries only")
	}
	cmdIndex([]string{"."}, map[string]string{"quiet": "true"})
	_, idx, err := openIndex()
	if err != nil {
		return err
	}
	defer idx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '3s'; SET LOCAL lock_timeout = '1s'"); err != nil {
		return err
	}
	skip := func(s string) bool {
		return dbSkipSchemas[s] || strings.HasPrefix(s, "pg_") || strings.HasPrefix(s, "_")
	}

	// ---- o banco ----
	type tbl struct{ rls bool }
	dbT := map[string]tbl{}
	rows, err := tx.Query(ctx, `SELECT n.nspname, c.relname, c.relrowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r','p')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s, n string
		var r bool
		rows.Scan(&s, &n, &r)
		if !skip(s) {
			dbT[s+"."+n] = tbl{r}
		}
	}
	rows.Close()
	dbP := map[string]string{} // "pol on schema.table" -> "cmd roles"
	rows, err = tx.Query(ctx, `SELECT schemaname, tablename, policyname, cmd, array_to_string(roles, ','), coalesce(qual,''), coalesce(with_check,'') FROM pg_policies`)
	if err != nil {
		return err
	}
	type pol struct{ table, cmd, roles, qual, check string }
	var dbPols []pol
	for rows.Next() {
		var s, t, n, cmd, roles, q, c string
		rows.Scan(&s, &t, &n, &cmd, &roles, &q, &c)
		if skip(s) {
			continue
		}
		dbP[n+" on "+s+"."+t] = strings.ToUpper(cmd) + " " + roles
		dbPols = append(dbPols, pol{s + "." + t, strings.ToUpper(cmd), roles, q, c})
	}
	rows.Close()
	dbF := map[string]bool{} // schema.name -> security definer
	rows, err = tx.Query(ctx, `SELECT n.nspname, p.proname, p.prosecdef FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE p.prokind = 'f' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s, n string
		var sd bool
		rows.Scan(&s, &n, &sd)
		if !skip(s) {
			dbF[s+"."+n] = sd
		}
	}
	rows.Close()

	// ---- as migrações (o índice) ----
	mT := map[string]tbl{}
	mSrc := map[string]string{}
	if r, err := idx.Query(`SELECT schema, name, rls, src FROM sql_tables WHERE is_view = 0`); err == nil {
		for r.Next() {
			var s, n, src string
			var rls int
			r.Scan(&s, &n, &rls, &src)
			if s == "" {
				s = "public"
			}
			mT[s+"."+n] = tbl{rls == 1}
			mSrc[s+"."+n] = src
		}
		r.Close()
	}
	mP := map[string]string{}
	if r, err := idx.Query(`SELECT schema, table_name, name, cmd, roles, src FROM sql_policies`); err == nil {
		for r.Next() {
			var s, t, n, cmd, roles, src string
			r.Scan(&s, &t, &n, &cmd, &roles, &src)
			if s == "" {
				s = "public"
			}
			k := n + " on " + s + "." + t
			mP[k] = strings.ToUpper(cmd)
			mSrc[k] = src
		}
		r.Close()
	}
	mF := map[string]bool{}
	if r, err := idx.Query(`SELECT schema, name, secdef, src FROM sql_functions`); err == nil {
		for r.Next() {
			var s, n, src string
			var sd int
			r.Scan(&s, &n, &sd, &src)
			if s == "" {
				s = "public"
			}
			mF[s+"."+n] = sd == 1
			mSrc[s+"."+n] = src
		}
		r.Close()
	}

	rep := dbReport{Tables: len(dbT), Drift: []dbItem{}}
	hasMigr := len(mT)+len(mP)+len(mF) > 0
	for k, t := range dbT {
		m, ok := mT[k]
		switch {
		case !ok && hasMigr:
			rep.Drift = append(rep.Drift, dbItem{"table", k, "db", "exists in the database but in no migration (created in the dashboard?)", ""})
		case ok && m.rls != t.rls:
			rep.Drift = append(rep.Drift, dbItem{"rls", k, "both", fmt.Sprintf("RLS is %s in the database and %s in the migrations", onOff(t.rls), onOff(m.rls)), mSrc[k]})
		}
	}
	for k := range mT {
		if _, ok := dbT[k]; !ok {
			rep.Drift = append(rep.Drift, dbItem{"table", k, "migrations", "in the migrations but not in the database (migration not applied?)", mSrc[k]})
		}
	}
	for k, v := range dbP {
		if _, ok := mP[k]; !ok && hasMigr {
			rep.Drift = append(rep.Drift, dbItem{"policy", k, "db", "policy only in the database (" + v + ")", ""})
		}
	}
	for k := range mP {
		if _, ok := dbP[k]; !ok {
			rep.Drift = append(rep.Drift, dbItem{"policy", k, "migrations", "policy in the migrations but not in the database", mSrc[k]})
		}
	}
	for k, sd := range dbF {
		m, ok := mF[k]
		switch {
		case !ok && hasMigr:
			d := "function only in the database"
			if sd {
				d += ", SECURITY DEFINER"
			}
			rep.Drift = append(rep.Drift, dbItem{"function", k, "db", d, ""})
		case ok && m != sd:
			rep.Drift = append(rep.Drift, dbItem{"secdef", k, "both", fmt.Sprintf("SECURITY DEFINER is %s in the database and %s in the migrations", onOff(sd), onOff(m)), mSrc[k]})
		}
	}
	for k := range mF {
		if _, ok := dbF[k]; !ok {
			rep.Drift = append(rep.Drift, dbItem{"function", k, "migrations", "function in the migrations but not in the database", mSrc[k]})
		}
	}
	sort.Slice(rep.Drift, func(i, j int) bool {
		if rep.Drift[i].Kind != rep.Drift[j].Kind {
			return rep.Drift[i].Kind < rep.Drift[j].Kind
		}
		return rep.Drift[i].Name < rep.Drift[j].Name
	})
	if !hasMigr {
		rep.Skipped = "no SQL migrations in this project: only the RLS test can run"
	}

	// ---- teste de RLS: o que anon e um usuário logado qualquer leem ----
	if flags["rls"] != "" {
		var roles []string
		for _, r := range []string{"anon", "authenticated"} {
			var ok bool
			if tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1) AND pg_has_role(current_user, $1, 'MEMBER')`, r).Scan(&ok) == nil && ok {
				roles = append(roles, r)
			}
		}
		if len(roles) == 0 {
			rep.Skipped = strings.TrimSpace(rep.Skipped + "; no anon/authenticated role this user can switch to (not Supabase?)")
		}
		var names []string
		for k := range dbT {
			if strings.HasPrefix(k, "public.") {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		if len(names) > 300 {
			names = names[:300]
		}
		claims := `{"sub":"00000000-0000-4000-8000-00000000c0d0","role":"%s","aud":"authenticated"}`
		for _, t := range names {
			for _, role := range roles {
				p := rlsProbe{Table: t, Role: role}
				if _, err := tx.Exec(ctx, "SAVEPOINT probe"); err != nil {
					return err
				}
				qt := pgx.Identifier(strings.SplitN(t, ".", 2)).Sanitize()
				var reads bool
				_, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL ROLE %s", pgx.Identifier{role}.Sanitize()))
				if err == nil {
					tx.Exec(ctx, "SELECT set_config('request.jwt.claims', $1, true)", fmt.Sprintf(claims, role))
					err = tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+qt+" LIMIT 1)").Scan(&reads)
				}
				if err != nil && !strings.Contains(err.Error(), "permission denied") {
					p.Err = shortErr(err) // sem permissão nenhuma = não lê, o que é o certo
				}
				p.Reads = reads
				tx.Exec(ctx, "ROLLBACK TO SAVEPOINT probe")
				// gravar: privilégio da tabela + política aberta (true) para o papel
				var w []string
				for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
					var can bool
					tx.QueryRow(ctx, "SELECT has_table_privilege($1, $2, $3)", role, t, op).Scan(&can)
					if !can {
						continue
					}
					if !dbT[t].rls {
						w = append(w, op)
						continue
					}
					for _, pl := range dbPols {
						open := strings.TrimSpace(strings.ToLower(pl.qual)) == "true" || strings.TrimSpace(strings.ToLower(pl.check)) == "true"
						if pl.table == t && (pl.cmd == op || pl.cmd == "ALL") && (strings.Contains(pl.roles, role) || strings.Contains(pl.roles, "public")) && open {
							w = append(w, op)
							break
						}
					}
				}
				p.Writes = strings.Join(w, ",")
				if p.Writes != "" {
					p.Why = map[bool]string{true: "open policy", false: "no RLS"}[dbT[t].rls]
				}
				if p.Reads || p.Writes != "" || p.Err != "" {
					rep.RLS = append(rep.RLS, p)
				}
			}
		}
	}

	if flags["json"] != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	fmt.Fprintf(out, "%d tables in the database (system schemas left out). Read-only checks.\n", rep.Tables)
	if rep.Skipped != "" {
		fmt.Fprintln(out, "note:", strings.TrimPrefix(rep.Skipped, "; "))
	}
	if len(rep.Drift) == 0 && hasMigr {
		fmt.Fprintln(out, "\nthe database matches the migrations")
	} else if len(rep.Drift) > 0 {
		fmt.Fprintf(out, "\ndatabase × migrations (%d):\n", len(rep.Drift))
		for _, d := range rep.Drift {
			src := ""
			if d.Src != "" {
				src = "  [" + d.Src + "]"
			}
			fmt.Fprintf(out, "  %-9s %-44s %s%s\n", d.Kind, d.Name, d.Detail, src)
		}
	}
	if flags["rls"] != "" {
		if len(rep.RLS) == 0 {
			fmt.Fprintln(out, "\nRLS test: anon and a random signed-in user read nothing and write nothing in public")
		} else {
			fmt.Fprintln(out, "\nRLS test (anon, and a random signed-in user that owns nothing):")
			for _, p := range rep.RLS {
				var what []string
				if p.Reads {
					what = append(what, "READS rows")
				}
				if p.Writes != "" {
					what = append(what, "can "+p.Writes+" ("+p.Why+")")
				}
				if p.Err != "" {
					what = append(what, "error: "+p.Err)
				}
				fmt.Fprintf(out, "  %-14s %-40s %s\n", p.Role, p.Table, strings.Join(what, "; "))
			}
		}
	}
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
