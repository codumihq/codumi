// Package sqlschema replays SQL migrations (in order) and keeps the final
// state of what matters for access control on Supabase: tables and views,
// RLS, policies, functions and their EXECUTE grants, and storage buckets.
//
// It is a pragmatic parser for migration files, not a full SQL parser:
// statements it doesn't understand are skipped, and dynamic SQL (EXECUTE
// inside DO blocks) is reported as a warning.
package sqlschema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

type fn struct {
	f    *model.Function
	exec map[string]bool
	body string
}

type trig struct{ fn, src string }

type parser struct {
	triggers     map[string]trig
	tables       map[string]*model.Table
	policies     map[string]*model.Policy
	funcs        map[string]*fn
	buckets      map[string]*model.Bucket
	defFnExec    map[string]bool
	defTableAnon bool
	dynamicSQL   int
	files        int
	stmts        int
}

func newParser() *parser {
	return &parser{
		tables:   map[string]*model.Table{},
		policies: map[string]*model.Policy{},
		funcs:    map[string]*fn{},
		buckets:  map[string]*model.Bucket{},
		triggers: map[string]trig{},
		// Postgres grants EXECUTE to PUBLIC on new functions, and Supabase's
		// default privileges grant anon/authenticated/service_role on the
		// public schema too.
		defFnExec:    map[string]bool{"public": true, "anon": true, "authenticated": true, "service_role": true},
		defTableAnon: true,
	}
}

// FindMigrations returns the .sql migration files under root, in apply order.
func FindMigrations(root string) []string {
	var dirs []string
	if st, err := os.Stat(filepath.Join(root, "supabase", "migrations")); err == nil && st.IsDir() {
		dirs = append(dirs, filepath.Join(root, "supabase", "migrations"))
	} else {
		base := strings.Count(filepath.Clean(root), string(filepath.Separator))
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			switch d.Name() {
			case "node_modules", ".git", ".next", "dist", "build", ".codumi", "vendor":
				return filepath.SkipDir
			}
			if strings.Count(p, string(filepath.Separator))-base > 4 {
				return filepath.SkipDir
			}
			if d.Name() == "migrations" {
				dirs = append(dirs, p)
				return filepath.SkipDir
			}
			return nil
		})
	}
	var files []string
	for _, d := range dirs {
		m, _ := filepath.Glob(filepath.Join(d, "*.sql"))
		sort.Strings(m)
		files = append(files, m...)
	}
	return files
}

// Load replays the migration files into g.
func Load(root string, files []string, g *model.Graph) error {
	p := newParser()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, f)
		rel = filepath.ToSlash(rel)
		p.files++
		for _, st := range Split(string(b)) {
			p.stmts++
			p.exec(norm(st.Skel), st.Bodies, fmt.Sprintf("%s:%d", rel, st.Line))
		}
	}
	p.finish(g)
	return nil
}

var reSpace = regexp.MustCompile(`\s+`)

func norm(s string) string {
	return strings.ToLower(strings.TrimSpace(reSpace.ReplaceAllString(s, " ")))
}

func isWord(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}

func readIdent(s string) (string, string) {
	s = strings.TrimLeft(s, " ")
	if s == "" {
		return "", ""
	}
	if s[0] == '"' {
		j := strings.IndexByte(s[1:], '"')
		if j < 0 {
			return "", s
		}
		return s[1 : 1+j], s[2+j:]
	}
	j := 0
	for j < len(s) && isWord(s[j]) && s[j] != '-' {
		j++
	}
	return s[:j], s[j:]
}

func readQName(s string) (schema, name, rest string) {
	a, r := readIdent(s)
	if a == "" {
		return "", "", s
	}
	if strings.HasPrefix(r, ".") {
		b, r2 := readIdent(r[1:])
		return a, b, r2
	}
	return "public", a, r
}

// splitTop splits on commas that are not inside parens, brackets or quotes.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '\'' || c == '"':
			q = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if t := strings.TrimSpace(s[start:]); t != "" {
		out = append(out, t)
	}
	return out
}

// matchParen returns the index just after the paren group that starts at s[0].
func matchParen(s string) int {
	depth := 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '\'' || c == '"':
			q = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

func roles(s string) []string {
	var out []string
	for _, r := range strings.Split(s, ",") {
		r = strings.Trim(strings.TrimSpace(r), `"`)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

func key(schema, name string) string { return schema + "." + name }

var (
	reCreateTable  = regexp.MustCompile(`^create (?:unlogged )?table (?:if not exists )?`)
	reCreateView   = regexp.MustCompile(`^create (?:or replace )?(?:materialized )?view (?:if not exists )?`)
	reDropTable    = regexp.MustCompile(`^drop (?:table|view|materialized view) (?:if exists )?`)
	reAlterTable   = regexp.MustCompile(`^alter (?:table|view) (?:if exists )?(?:only )?`)
	reCreatePol    = regexp.MustCompile(`^create policy `)
	rePolTail      = regexp.MustCompile(`(?s)^(?:as (permissive|restrictive) ?)?(?:for (all|select|insert|update|delete) ?)?(?:to (.+?) ?)?(?:using ?(\(.*?\)) ?)?(?:with check ?(\(.*\)))? ?$`)
	reAlterPolTail = regexp.MustCompile(`(?s)^(?:to (.+?) ?)?(?:using ?(\(.*?\)) ?)?(?:with check ?(\(.*\)))? ?$`)
	reDropPol      = regexp.MustCompile(`^drop policy (?:if exists )?`)
	reAlterPol     = regexp.MustCompile(`^alter policy `)
	reCreateTrig   = regexp.MustCompile(`^create (?:or replace )?(?:constraint )?trigger (\S+) .*? on ((?:[a-z_][a-z0-9_]*\.)?"?[a-z_][a-z0-9_]*"?) .*?execute (?:function|procedure) ((?:[a-z_][a-z0-9_]*\.)?[a-z_][a-z0-9_]*) ?\(`)
	reDropTrig     = regexp.MustCompile(`^drop trigger (?:if exists )?(\S+) on ((?:[a-z_][a-z0-9_]*\.)?"?[a-z_][a-z0-9_]*"?)`)
	reCreateFn     = regexp.MustCompile(`^create (or replace )?function `)
	reReturns      = regexp.MustCompile(`returns (setof )?(table\b|[a-z0-9_."\[\]]+)`)
	reDropFn       = regexp.MustCompile(`^drop function (?:if exists )?`)
	reAlterFn      = regexp.MustCompile(`^alter function `)
	reGrant        = regexp.MustCompile(`^(grant|revoke) (?:grant option for )?(.+?) on (.+?) (to|from) (.+?)(?: with grant option| granted by \S+| cascade| restrict)*$`)
	reDefPriv      = regexp.MustCompile(`^alter default privileges (?:for (?:role|user) .+? )?(?:in schema .+? )?(grant|revoke) (?:grant option for )?(.+?) on (functions|routines|tables) (to|from) (.+?)(?: with grant option| cascade| restrict)?$`)
	reBucketIns    = regexp.MustCompile(`(?s)^insert into storage\.buckets ?\(([^)]*)\) ?values ?(.+?)(?: on conflict\b.*)?$`)
	reBucketUpd    = regexp.MustCompile(`(?s)^update storage\.buckets set (.+?) where (.+)$`)
	reBucketDel    = regexp.MustCompile(`(?s)^delete from storage\.buckets where (.+)$`)
	reSetPublic    = regexp.MustCompile(`\bpublic ?= ?(true|false)`)
	reWhereID      = regexp.MustCompile(`\b(?:id|name) ?(?:= ?'([^']+)'|in ?\(([^)]*)\))`)
	reCtlPrefix    = regexp.MustCompile(`^(?:(?:begin|else|loop|then) |(?:if|elsif) .+? then |exception when .+? then |declare .+? begin )+`)
	reCallerCheck  = regexp.MustCompile(`auth\.uid\(\)|auth\.jwt\(\)|auth\.role\(\)|request\.jwt\.claim|current_user|session_user`)
	reInvoker      = regexp.MustCompile(`security_invoker ?= ?(?:true|on|'?true'?|1)`)
)

func (p *parser) exec(s string, bodies []string, src string) {
	switch {
	case strings.HasPrefix(s, "do ") || s == "do":
		for _, b := range bodies {
			if strings.Contains(strings.ToLower(b), "execute ") {
				p.dynamicSQL++
			}
			for _, inner := range Split(b) {
				t := reCtlPrefix.ReplaceAllString(norm(inner.Skel), "")
				p.exec(t, inner.Bodies, src)
			}
		}
	case reCreateView.MatchString(s):
		rest := reCreateView.ReplaceAllString(s, "")
		sc, name, tail := readQName(rest)
		if name == "" {
			return
		}
		head := tail
		if i := strings.Index(head, " as "); i >= 0 {
			head = head[:i]
		}
		p.tables[key(sc, name)] = &model.Table{Schema: sc, Name: name, View: true, Invoker: reInvoker.MatchString(head), AnonGrant: p.defTableAnon, Src: src}
	case reCreateTable.MatchString(s):
		sc, name, _ := readQName(reCreateTable.ReplaceAllString(s, ""))
		if name == "" {
			return
		}
		if _, ok := p.tables[key(sc, name)]; !ok {
			p.tables[key(sc, name)] = &model.Table{Schema: sc, Name: name, AnonGrant: p.defTableAnon, Src: src}
		}
	case reDropTable.MatchString(s):
		rest := strings.TrimSuffix(strings.TrimSuffix(reDropTable.ReplaceAllString(s, ""), " cascade"), " restrict")
		for _, part := range splitTop(rest) {
			if sc, name, _ := readQName(part); name != "" {
				delete(p.tables, key(sc, name))
				for k, pol := range p.policies {
					if pol.Schema == sc && pol.Table == name {
						delete(p.policies, k)
					}
				}
			}
		}
	case reAlterTable.MatchString(s):
		sc, name, tail := readQName(reAlterTable.ReplaceAllString(s, ""))
		t := p.tables[key(sc, name)]
		if t == nil {
			return
		}
		tail = strings.TrimSpace(tail)
		switch {
		case strings.Contains(tail, "disable row level security"):
			t.RLS = false
		case strings.Contains(tail, "enable row level security"):
			t.RLS = true
		case strings.HasPrefix(tail, "rename to "):
			nn, _ := readIdent(strings.TrimPrefix(tail, "rename to "))
			if nn == "" {
				return
			}
			delete(p.tables, key(sc, name))
			t.Name = nn
			p.tables[key(sc, nn)] = t
			for k, pol := range p.policies {
				if pol.Schema == sc && pol.Table == name {
					delete(p.policies, k)
					pol.Table = nn
					p.policies[key(sc, nn)+"."+pol.Name] = pol
				}
			}
		case strings.HasPrefix(tail, "set (") && t.View:
			t.Invoker = reInvoker.MatchString(tail)
		}
	case reCreatePol.MatchString(s):
		name, rest := readIdent(reCreatePol.ReplaceAllString(s, ""))
		rest = strings.TrimSpace(rest)
		if name == "" || !strings.HasPrefix(rest, "on ") {
			return
		}
		sc, table, tail := readQName(rest[3:])
		m := rePolTail.FindStringSubmatch(strings.TrimSpace(tail))
		if m == nil {
			return
		}
		pol := &model.Policy{Schema: sc, Table: table, Name: name, Cmd: "all", Permissive: m[1] != "restrictive", Using: m[4], Check: m[5], Src: src}
		if m[2] != "" {
			pol.Cmd = m[2]
		}
		pol.Roles = roles(m[3])
		if len(pol.Roles) == 0 {
			pol.Roles = []string{"public"}
		}
		p.policies[key(sc, table)+"."+name] = pol
	case reDropPol.MatchString(s):
		name, rest := readIdent(reDropPol.ReplaceAllString(s, ""))
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "on ") {
			sc, table, _ := readQName(rest[3:])
			delete(p.policies, key(sc, table)+"."+name)
		}
	case reAlterPol.MatchString(s):
		name, rest := readIdent(reAlterPol.ReplaceAllString(s, ""))
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "on ") {
			return
		}
		sc, table, tail := readQName(rest[3:])
		k := key(sc, table) + "." + name
		pol := p.policies[k]
		if pol == nil {
			return
		}
		tail = strings.TrimSpace(tail)
		if strings.HasPrefix(tail, "rename to ") {
			nn, _ := readIdent(strings.TrimPrefix(tail, "rename to "))
			delete(p.policies, k)
			pol.Name = nn
			p.policies[key(sc, table)+"."+nn] = pol
			return
		}
		if m := reAlterPolTail.FindStringSubmatch(tail); m != nil {
			if m[1] != "" {
				pol.Roles = roles(m[1])
			}
			if m[2] != "" {
				pol.Using = m[2]
			}
			if m[3] != "" {
				pol.Check = m[3]
			}
			pol.Src = src
		}
	case reCreateTrig.MatchString(s):
		m := reCreateTrig.FindStringSubmatch(s)
		tbl := strings.ReplaceAll(m[2], `"`, "")
		if !strings.Contains(tbl, ".") {
			tbl = "public." + tbl
		}
		fn := m[3][strings.LastIndex(m[3], ".")+1:]
		p.triggers[strings.Trim(m[1], `"`)+" on "+tbl] = trig{fn: fn, src: src}
	case reDropTrig.MatchString(s):
		m := reDropTrig.FindStringSubmatch(s)
		tbl := strings.ReplaceAll(m[2], `"`, "")
		if !strings.Contains(tbl, ".") {
			tbl = "public." + tbl
		}
		delete(p.triggers, strings.Trim(m[1], `"`)+" on "+tbl)
	case reCreateFn.MatchString(s):
		replace := reCreateFn.FindStringSubmatch(s)[1] != ""
		sc, name, tail := readQName(reCreateFn.ReplaceAllString(s, ""))
		if name == "" {
			return
		}
		tail = strings.TrimSpace(tail)
		fnArgs := ""
		if strings.HasPrefix(tail, "(") {
			end := matchParen(tail)
			fnArgs = strings.TrimSpace(tail[1 : end-1])
			tail = tail[end:]
		}
		ret := ""
		if m := reReturns.FindStringSubmatch(tail); m != nil {
			ret = strings.TrimSpace(m[1] + m[2])
		}
		secdef := strings.Contains(tail, "security definer")
		scoped := reCallerCheck.MatchString(strings.ToLower(strings.Join(bodies, "\n")))
		k := key(sc, name)
		if cur, ok := p.funcs[k]; ok {
			cur.f.Returns = ret
			cur.f.Args = fnArgs
			if replace {
				cur.f.SecDef, cur.f.CallerScoped = secdef, scoped
			} else {
				cur.f.SecDef = cur.f.SecDef || secdef
				cur.f.CallerScoped = cur.f.CallerScoped && scoped
			}
			cur.f.Src = src
			cur.body = strings.ToLower(strings.Join(bodies, "\n"))
			return
		}
		exec := map[string]bool{}
		for r := range p.defFnExec {
			exec[r] = true
		}
		p.funcs[k] = &fn{f: &model.Function{Schema: sc, Name: name, Args: fnArgs, Returns: ret, SecDef: secdef, CallerScoped: scoped, Src: src}, exec: exec, body: strings.ToLower(strings.Join(bodies, "\n"))}
	case reDropFn.MatchString(s):
		rest := strings.TrimSuffix(strings.TrimSuffix(reDropFn.ReplaceAllString(s, ""), " cascade"), " restrict")
		for _, part := range splitTop(rest) {
			if sc, name, _ := readQName(part); name != "" {
				delete(p.funcs, key(sc, name))
			}
		}
	case reAlterFn.MatchString(s):
		sc, name, tail := readQName(reAlterFn.ReplaceAllString(s, ""))
		cur := p.funcs[key(sc, name)]
		if cur == nil {
			return
		}
		if strings.Contains(tail, "security definer") {
			cur.f.SecDef = true
		} else if strings.Contains(tail, "security invoker") {
			cur.f.SecDef = false
		}
	case reDefPriv.MatchString(s):
		m := reDefPriv.FindStringSubmatch(s)
		grant, privs, obj, rs := m[1] == "grant", m[2], m[3], roles(m[5])
		if obj == "tables" {
			if hasPriv(privs, "select") {
				for _, r := range rs {
					if r == "anon" || r == "public" {
						p.defTableAnon = grant
					}
				}
			}
			return
		}
		if hasPriv(privs, "execute") {
			for _, r := range rs {
				if grant {
					p.defFnExec[r] = true
				} else {
					delete(p.defFnExec, r)
				}
			}
		}
	case reGrant.MatchString(s):
		m := reGrant.FindStringSubmatch(s)
		p.grant(m[1] == "grant", m[2], m[3], roles(m[5]))
	case reBucketIns.MatchString(s):
		m := reBucketIns.FindStringSubmatch(s)
		cols := roles(m[1])
		idIdx, pubIdx := -1, -1
		for i, c := range cols {
			switch c {
			case "id":
				idIdx = i
			case "name":
				if idIdx < 0 {
					idIdx = i
				}
			case "public":
				pubIdx = i
			}
		}
		if idIdx < 0 {
			return
		}
		for _, tuple := range tuples(m[2]) {
			vals := splitTop(tuple)
			if idIdx >= len(vals) {
				continue
			}
			id := literal(vals[idIdx])
			pub := pubIdx >= 0 && pubIdx < len(vals) && literal(vals[pubIdx]) == "true"
			p.buckets[id] = &model.Bucket{ID: id, Public: pub, Src: src}
		}
	case reBucketUpd.MatchString(s):
		m := reBucketUpd.FindStringSubmatch(s)
		pm := reSetPublic.FindStringSubmatch(m[1])
		if pm == nil {
			return
		}
		for _, id := range whereIDs(m[2]) {
			if b := p.buckets[id]; b != nil {
				b.Public = pm[1] == "true"
				b.Src = src
			}
		}
	case reBucketDel.MatchString(s):
		for _, id := range whereIDs(reBucketDel.FindStringSubmatch(s)[1]) {
			delete(p.buckets, id)
		}
	}
}

func hasPriv(privs, want string) bool {
	for _, x := range strings.Split(privs, ",") {
		x = strings.TrimSpace(x)
		if x == want || x == "all" || x == "all privileges" {
			return true
		}
	}
	return false
}

func (p *parser) grant(grant bool, privs, obj string, rs []string) {
	setFn := func(f *fn) {
		for _, r := range rs {
			if grant {
				f.exec[r] = true
			} else {
				delete(f.exec, r)
			}
		}
	}
	switch {
	case strings.HasPrefix(obj, "all functions in schema ") || strings.HasPrefix(obj, "all routines in schema "):
		if !hasPriv(privs, "execute") {
			return
		}
		for _, sch := range roles(obj[strings.LastIndex(obj, "schema ")+7:]) {
			for _, f := range p.funcs {
				if f.f.Schema == sch {
					setFn(f)
				}
			}
		}
	case strings.HasPrefix(obj, "function ") || strings.HasPrefix(obj, "routine "):
		if !hasPriv(privs, "execute") {
			return
		}
		obj = obj[strings.IndexByte(obj, ' ')+1:]
		for _, part := range splitTop(obj) {
			if sc, name, _ := readQName(part); name != "" {
				if f := p.funcs[key(sc, name)]; f != nil {
					setFn(f)
				}
			}
		}
	case strings.HasPrefix(obj, "schema ") || strings.HasPrefix(obj, "sequence ") || strings.HasPrefix(obj, "all sequences ") ||
		strings.HasPrefix(obj, "procedure ") || strings.HasPrefix(obj, "type ") || strings.HasPrefix(obj, "domain "):
		return
	default:
		if !hasPriv(privs, "select") {
			return
		}
		var targets []*model.Table
		if strings.HasPrefix(obj, "all tables in schema ") {
			for _, sch := range roles(strings.TrimPrefix(obj, "all tables in schema ")) {
				for _, t := range p.tables {
					if t.Schema == sch {
						targets = append(targets, t)
					}
				}
			}
		} else {
			obj = strings.TrimPrefix(obj, "table ")
			for _, part := range splitTop(obj) {
				if sc, name, _ := readQName(part); name != "" {
					if t := p.tables[key(sc, name)]; t != nil {
						targets = append(targets, t)
					}
				}
			}
		}
		for _, r := range rs {
			if r == "anon" || r == "public" {
				for _, t := range targets {
					t.AnonGrant = grant
				}
			}
		}
	}
}

// tuples returns the inside of each top-level (...) group in a VALUES list.
func tuples(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '(')
		if i < 0 {
			return out
		}
		end := i + matchParen(s[i:])
		out = append(out, s[i+1:end-1])
		s = s[end:]
	}
}

func literal(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "::"); i >= 0 {
		v = v[:i]
	}
	return strings.Trim(v, `'"`)
}

func whereIDs(where string) []string {
	var ids []string
	for _, m := range reWhereID.FindAllStringSubmatch(where, -1) {
		if m[1] != "" {
			ids = append(ids, m[1])
		}
		for _, x := range splitTop(m[2]) {
			ids = append(ids, literal(x))
		}
	}
	return ids
}

func (p *parser) finish(g *model.Graph) {
	// A function that calls a caller-checking helper (e.g. is_account_member())
	// checks the caller too.
	for changed := true; changed; {
		changed = false
		for _, f := range p.funcs {
			if f.f.CallerScoped {
				continue
			}
			for _, h := range p.funcs {
				if h.f.CallerScoped && h != f && strings.Contains(f.body, h.f.Name+"(") {
					f.f.CallerScoped, changed = true, true
					break
				}
			}
		}
	}
	for _, t := range p.tables {
		g.Tables = append(g.Tables, *t)
	}
	for _, pol := range p.policies {
		g.Policies = append(g.Policies, *pol)
	}
	for _, f := range p.funcs {
		ff := *f.f
		for r := range f.exec {
			ff.Exec = append(ff.Exec, r)
		}
		sort.Strings(ff.Exec)
		g.Functions = append(g.Functions, ff)
	}
	for _, b := range p.buckets {
		g.Buckets = append(g.Buckets, *b)
	}
	// Uses from inside the database: function bodies, triggers and policies.
	for name, t := range p.triggers {
		g.SQLRefs = append(g.SQLRefs, model.SQLRef{Kind: "trigger", Name: name, To: t.fn, Src: t.src})
	}
	for _, h := range p.funcs {
		call, err := regexp.Compile(`\b` + regexp.QuoteMeta(h.f.Name) + ` ?\(`)
		if err != nil {
			continue
		}
		needle := h.f.Name
		for _, f := range p.funcs {
			if f != h && strings.Contains(f.body, needle) && call.MatchString(f.body) {
				g.SQLRefs = append(g.SQLRefs, model.SQLRef{Kind: "function", Name: f.f.Schema + "." + f.f.Name + "()", To: h.f.Name, Src: f.f.Src})
			}
		}
		for _, pol := range p.policies {
			if expr := pol.Using + " " + pol.Check; strings.Contains(expr, needle) && call.MatchString(expr) {
				g.SQLRefs = append(g.SQLRefs, model.SQLRef{Kind: "policy", Name: `"` + pol.Name + `" on ` + pol.Schema + "." + pol.Table, To: h.f.Name, Src: pol.Src})
			}
		}
	}
	sort.Slice(g.SQLRefs, func(i, j int) bool {
		if g.SQLRefs[i].To != g.SQLRefs[j].To {
			return g.SQLRefs[i].To < g.SQLRefs[j].To
		}
		return g.SQLRefs[i].Kind+g.SQLRefs[i].Name < g.SQLRefs[j].Kind+g.SQLRefs[j].Name
	})
	sort.Slice(g.Tables, func(i, j int) bool {
		return key(g.Tables[i].Schema, g.Tables[i].Name) < key(g.Tables[j].Schema, g.Tables[j].Name)
	})
	sort.Slice(g.Policies, func(i, j int) bool { return g.Policies[i].Src < g.Policies[j].Src })
	sort.Slice(g.Functions, func(i, j int) bool {
		return key(g.Functions[i].Schema, g.Functions[i].Name) < key(g.Functions[j].Schema, g.Functions[j].Name)
	})
	sort.Slice(g.Buckets, func(i, j int) bool { return g.Buckets[i].ID < g.Buckets[j].ID })
	if p.dynamicSQL > 0 {
		g.Warnings = append(g.Warnings, fmt.Sprintf("%d DO block(s) run dynamic SQL (EXECUTE); what they create is not tracked", p.dynamicSQL))
	}
	g.Warnings = append(g.Warnings, fmt.Sprintf("sql: %d migration file(s), %d statement(s)", p.files, p.stmts))
}
