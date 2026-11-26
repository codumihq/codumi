// Package audit checks a Supabase project for common access-control holes,
// using the final schema replayed from migrations plus the code graph.
package audit

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

type Severity int

const (
	Low Severity = iota
	Medium
	High
)

func (s Severity) String() string {
	return [...]string{"LOW", "MEDIUM", "HIGH"}[s]
}

type Finding struct {
	Severity  Severity `json:"-"`
	Level     string   `json:"severity"`
	Rule      string   `json:"rule"`
	Target    string   `json:"target"`
	Message   string   `json:"message"`
	Where     string   `json:"where"`
	Fix       string   `json:"fix,omitempty"`
	Heuristic bool     `json:"heuristic,omitempty"`
	UsedIn    []string `json:"used_in,omitempty"`
}

func roleOpen(roles []string) bool {
	for _, r := range roles {
		if r == "anon" || r == "public" {
			return true
		}
	}
	return false
}

func onlyAuth(roles []string) bool {
	for _, r := range roles {
		if r != "authenticated" {
			return false
		}
	}
	return len(roles) > 0
}

var reStrip = regexp.MustCompile(`[\s()]`)

func isTrue(expr string) bool { return reStrip.ReplaceAllString(expr, "") == "true" }

var (
	reBucketID   = regexp.MustCompile(`bucket_id ?= ?'([^']+)'(?:::text)?`)
	reOnlyGlue   = regexp.MustCompile(`^[\s()]*(?:(?:and|or)[\s()]*)*$`)
	reWebhook    = regexp.MustCompile(`(?i)webhook|callback|/hooks?(/|$)`)
	reVerifyHint = regexp.MustCompile(`(?i)signature|hmac|timingSafeEqual|verif|secret|authorization|x-hub|bearer|svix|webhooks\.construct`)
	reServiceKey = regexp.MustCompile(`SERVICE_ROLE`)
	rePublicKey  = regexp.MustCompile(`NEXT_PUBLIC_[A-Z0-9_]*SERVICE_ROLE|VITE_[A-Z0-9_]*SERVICE_ROLE|EXPO_PUBLIC_[A-Z0-9_]*SERVICE_ROLE`)
)

// bucketScope tells which buckets a storage.objects expression is limited
// to, and whether bucket_id is its only condition.
func bucketScope(expr string) (ids []string, onlyBucket bool) {
	for _, m := range reBucketID.FindAllStringSubmatch(expr, -1) {
		ids = append(ids, m[1])
	}
	rest := reBucketID.ReplaceAllString(expr, "")
	return ids, reOnlyGlue.MatchString(rest)
}

// Run returns the findings, most severe first.
func Run(root string, g *model.Graph) []Finding {
	var fs []Finding
	uses := map[string][]string{} // type:name -> file:line (ops)
	for _, u := range g.DBUses {
		k := u.Type + ":" + strings.ToLower(u.Name)
		loc := u.File + ":" + itoa(u.Line)
		if len(u.Ops) > 0 {
			loc += " (" + strings.Join(u.Ops, ",") + ")"
		}
		uses[k] = append(uses[k], loc)
	}

	for _, t := range g.Tables {
		if t.Schema != "public" {
			continue
		}
		name := t.Schema + "." + t.Name
		if t.View {
			if !t.Invoker && t.AnonGrant {
				fs = append(fs, Finding{Severity: Medium, Rule: "view-security-definer", Target: name,
					Message: "View runs with its owner's rights, so it ignores the RLS of the tables it reads. Anyone with the anon key can query it.",
					Where:   t.Src, Fix: "alter view " + name + " set (security_invoker = true);", UsedIn: uses["table:"+t.Name]})
			}
			continue
		}
		if !t.RLS {
			if t.AnonGrant {
				fs = append(fs, Finding{Severity: High, Rule: "rls-disabled", Target: name,
					Message: "RLS is off: anyone with the anon key can read and change every row through the API.",
					Where:   t.Src, Fix: "alter table " + name + " enable row level security;  -- then add policies", UsedIn: uses["table:"+t.Name]})
			} else {
				fs = append(fs, Finding{Severity: Medium, Rule: "rls-disabled", Target: name,
					Message: "RLS is off. anon was revoked, but any signed-in user can read and change every row.",
					Where:   t.Src, Fix: "alter table " + name + " enable row level security;", UsedIn: uses["table:"+t.Name]})
			}
		}
	}

	for _, f := range g.Functions {
		if f.Schema != "public" || !f.SecDef || f.Returns == "trigger" || f.Returns == "event_trigger" {
			continue
		}
		if roleOpen(f.Exec) {
			name := f.Schema + "." + f.Name
			fx := "revoke execute on function " + name + " from public, anon;"
			if f.CallerScoped {
				fs = append(fs, Finding{Severity: Low, Rule: "security-definer-anon", Target: name + "()",
					Message: "SECURITY DEFINER function callable by anon. Its body checks the caller (auth.uid()/auth.jwt()), so anonymous calls should get nothing — revoke anyway.",
					Where:   f.Src, Fix: fx, UsedIn: uses["rpc:"+f.Name]})
				continue
			}
			fs = append(fs, Finding{Severity: High, Rule: "security-definer-anon", Target: name + "()",
				Message: "SECURITY DEFINER function runs with its owner's rights (bypassing RLS), never checks who is calling, and anon can call it at /rest/v1/rpc/" + f.Name + ".",
				Where:   f.Src, Fix: fx + "  -- and check ownership inside it", UsedIn: uses["rpc:"+f.Name]})
		}
	}

	buckets := map[string]model.Bucket{}
	for _, b := range g.Buckets {
		buckets[b.ID] = b
	}
	listed := map[string]bool{}
	for _, p := range g.Policies {
		if !p.Permissive {
			continue
		}
		if p.Schema == "storage" && p.Table == "objects" {
			if p.Cmd != "select" && p.Cmd != "all" {
				continue
			}
			ids, only := bucketScope(p.Using)
			if len(ids) == 0 && isTrue(p.Using) && roleOpen(p.Roles) {
				fs = append(fs, Finding{Severity: High, Rule: "storage-listable", Target: "storage.objects",
					Message: "Policy \"" + p.Name + "\" lets anyone list and read files in every bucket.", Where: p.Src})
				continue
			}
			if !only {
				continue
			}
			for _, id := range ids {
				switch {
				case roleOpen(p.Roles):
					listed[id] = true
					fs = append(fs, Finding{Severity: High, Rule: "storage-listable", Target: "bucket " + id,
						Message: "Policy \"" + p.Name + "\" only checks the bucket: anyone can list every file in it, not just open a known link.",
						Where:   p.Src, Fix: "restrict the policy (e.g. to the owner's folder) or drop it; public buckets serve files by URL without a SELECT policy",
						UsedIn: uses["bucket:"+id]})
				case onlyAuth(p.Roles):
					listed[id] = true
					fs = append(fs, Finding{Severity: Medium, Rule: "storage-listable", Target: "bucket " + id,
						Message: "Policy \"" + p.Name + "\" only checks the bucket: any signed-in user (of any account) can list every file in it.",
						Where:   p.Src, Fix: "add an ownership condition, e.g. (storage.foldername(name))[1] = auth.uid()::text",
						UsedIn: uses["bucket:"+id]})
				}
			}
			continue
		}
		if !roleOpen(p.Roles) || p.Schema != "public" {
			continue
		}
		target := p.Schema + "." + p.Table
		writes := p.Cmd == "insert" || p.Cmd == "update" || p.Cmd == "delete" || p.Cmd == "all"
		if writes && (isTrue(p.Check) || (p.Check == "" && isTrue(p.Using))) {
			fs = append(fs, Finding{Severity: High, Rule: "policy-open-write", Target: target,
				Message: "Policy \"" + p.Name + "\" (" + p.Cmd + ") lets anyone, signed in or not, write to this table.",
				Where:   p.Src, UsedIn: uses["table:"+p.Table]})
		} else if (p.Cmd == "select" || p.Cmd == "all") && isTrue(p.Using) {
			fs = append(fs, Finding{Severity: Low, Rule: "policy-open-read", Target: target,
				Message: "Policy \"" + p.Name + "\" lets anyone read every row. Fine for public catalogs; confirm it is intended.",
				Where:   p.Src, UsedIn: uses["table:"+p.Table]})
		}
	}
	for _, b := range g.Buckets {
		if b.Public && !listed[b.ID] {
			fs = append(fs, Finding{Severity: Low, Rule: "bucket-public", Target: "bucket " + b.ID,
				Message: "Public bucket: any file can be downloaded by anyone who has its URL. Don't keep private files here.",
				Where:   b.Src, UsedIn: uses["bucket:"+b.ID]})
		}
	}

	calls := map[[2]string][][2]string{}
	for _, r := range g.Refs {
		k := [2]string{r.File, r.From}
		calls[k] = append(calls[k], [2]string{r.ToFile, r.To})
	}
	// verifiedByCalls: the handler reaches (depth ≤ 3) a function that
	// verifies a signature or re-fetches the event from the provider.
	verifiedByCalls := func(file, handler string) bool {
		frontier := [][2]string{{file, handler}, {file, "<module>"}}
		seen := map[[2]string]bool{}
		for d := 0; d < 3 && len(frontier) > 0; d++ {
			var next [][2]string
			for _, n := range frontier {
				for _, t := range calls[n] {
					if seen[t] {
						continue
					}
					seen[t] = true
					short := t[1][strings.LastIndex(t[1], ".")+1:]
					if strings.HasPrefix(t[0], "ext:") {
						// stdlib/deps: hmac.Equal, subtle.ConstantTimeCompare, jwt.Parse…
						if reVerifyFn.MatchString(t[1]) || reExtVerify.MatchString(t[1]) {
							return true
						}
						continue
					}
					if reVerifyFn.MatchString(short) || reRefetchFn.MatchString(short) {
						return true
					}
					next = append(next, t)
				}
			}
			frontier = next
		}
		return false
	}
	seen := map[string]bool{}
	for _, r := range g.Routes {
		if !reWebhook.MatchString(r.Path) || seen[r.File+r.Method+r.Path] || r.Method == "GET" || r.Method == "OPTIONS" || r.Method == "HEAD" {
			continue
		}
		seen[r.File+r.Method+r.Path] = true
		hf, h := r.HandlerFile, r.Handler
		if hf == "" {
			hf, h = r.File, r.Method
		}
		// set/get/update… handlers manage webhook settings; they don't receive events.
		if h != r.Method && reManageFn.MatchString(h[strings.LastIndex(h, ".")+1:]) {
			continue
		}
		if guarded(r.Guards) {
			continue
		}
		if verifiedByCalls(hf, h) {
			continue
		}
		// TS route files hold one route each, so their text is a fair hint.
		// Go handler files hold many handlers, so only the call graph counts.
		if !strings.HasSuffix(hf, ".go") {
			b, err := os.ReadFile(filepath.Join(root, hf))
			if err != nil || reVerifyHint.Match(stripComments(b, hf)) {
				continue
			}
		}
		fs = append(fs, Finding{Severity: Medium, Rule: "webhook-unverified", Target: r.Method + " " + r.Path, Heuristic: true,
			Message: "Webhook handler shows no signature, secret or token check: anyone who finds the URL can send fake events.",
			Where:   r.File + ":" + itoa(r.Line), Fix: "verify a signature/HMAC or a secret in the URL or header before trusting the payload"})
	}

	fs = append(fs, noAuthRoutes(root, g)...)

	for _, f := range g.Files {
		if !strings.HasSuffix(f.Path, ".ts") && !strings.HasSuffix(f.Path, ".tsx") && !strings.HasSuffix(f.Path, ".js") && !strings.HasSuffix(f.Path, ".jsx") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f.Path))
		if err != nil {
			continue
		}
		if loc := rePublicKey.FindIndex(b); loc != nil {
			fs = append(fs, Finding{Severity: High, Rule: "service-role-public", Target: f.Path,
				Message: "The service role key is read from a public env var, so it ships to the browser and bypasses all RLS.",
				Where:   f.Path + ":" + itoa(lineAt(b, loc[0])), Fix: "use the service role key only on the server, from a non-public env var"})
			continue
		}
		head := strings.TrimSpace(string(b[:min(len(b), 200)]))
		if (strings.HasPrefix(head, `"use client"`) || strings.HasPrefix(head, `'use client'`)) && reServiceKey.Match(b) {
			loc := reServiceKey.FindIndex(b)
			fs = append(fs, Finding{Severity: High, Rule: "service-role-client", Target: f.Path,
				Message: "A client component references the service role key.",
				Where:   f.Path + ":" + itoa(lineAt(b, loc[0])), Fix: "move this to a server route or server action"})
		}
	}

	for i := range fs {
		fs[i].Level = fs[i].Severity.String()
	}
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Severity != fs[j].Severity {
			return fs[i].Severity > fs[j].Severity
		}
		return fs[i].Target < fs[j].Target
	})
	return fs
}

var (
	reVerifyFn     = regexp.MustCompile(`(?i)verif|signature|hmac|authenticat|constructEvent|checkSecret|checkToken|validateToken|^require(Auth|ApiKey|User|Session|Admin|Account|PlatformAdmin)|^getCurrent(User|Account)$|^getSession$|^getUser$`)
	reRefetchFn    = regexp.MustCompile(`(?i)^(get|fetch|retrieve|lookup|consult\w*|reconsult\w*|buscar|consultar)\w*(payment|order|charge|cobranca|pagamento|pedido|invoice|transaction|event|subscription)\w*$`)
	reExtVerify    = regexp.MustCompile(`^(hmac\.|subtle\.ConstantTime|jwt\.Parse|ed25519\.Verify|rsa\.Verify|ecdsa\.Verify)`)
	reManageFn     = regexp.MustCompile(`(?i)^(set|get|update|delete|create|list|patch|put|remove|register|configure|save|edit)`)
	reAuthGuard    = regexp.MustCompile(`(?i)^(auth|admin|require|protect|secure|authenticated|withauth|mustauth|apikey|basicauth|jwt|session|verify)|auth|login|signed_?in|logged_?in|current_?(user|account)|jwt|token|ensure_|permission|is_staff|superuser|PreAuthorize|Secured|RolesAllowed`)
	rePublicPath   = regexp.MustCompile(`(?i)login|logout|sign_?in|sign_?up|signout|register|registr|session|password|senha|webhook|callback|oauth|auth|health|saude|status|ping|contact|contato|newsletter|subscribe|cadastr|entrar|sair|esqueci|reset|verify|confirm`)
	reHandlerAuth  = regexp.MustCompile(`current_user|authenticate|authorize|login_required|Auth::|auth\(\)|request\.user|PreAuthorize|User\.Identity|get_current_user|require_login|conn\.assigns\.current_user|current_scope`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reLineComment  = regexp.MustCompile(`(?m)^\s*//.*$`)
	reHashComment  = regexp.MustCompile(`(?m)^\s*#.*$`) // Python, shell
)

// stripComments drops /* */ blocks and whole-line // comments, so a comment
// saying "there is no HMAC here" doesn't count as a check.
func stripComments(b []byte, path string) []byte {
	if strings.HasSuffix(path, ".py") {
		return reHashComment.ReplaceAll(b, nil)
	}
	return reLineComment.ReplaceAll(reBlockComment.ReplaceAll(b, nil), nil)
}

func guarded(guards string) bool {
	for _, g := range strings.Split(guards, ",") {
		if g != "" && reAuthGuard.MatchString(g) {
			return true
		}
	}
	return false
}

func lineAt(b []byte, off int) int { return strings.Count(string(b[:off]), "\n") + 1 }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// noAuthRoutes: rotas que escrevem no banco e não passam por nenhum login.
// Só vale onde o extrator sabe dizer (Guards preenchido): before_action do
// Rails, middleware do Laravel, pipe_through do Phoenix, [Authorize] do
// ASP.NET, Depends/decorators do FastAPI e Flask.
func noAuthRoutes(root string, g *model.Graph) []Finding {
	calls := map[string][]string{}
	for _, r := range g.Refs {
		if r.Kind == "call" || r.Kind == "new" {
			calls[r.From] = append(calls[r.From], r.To)
		}
	}
	writes := map[string][]string{} // declaração → tabelas em que escreve
	for _, u := range g.DBUses {
		for _, op := range u.Ops {
			if op == "insert" || op == "update" || op == "upsert" || op == "delete" {
				writes[u.From] = append(writes[u.From], u.Name+" ("+op+")")
				break
			}
		}
	}
	reach := func(h string) []string {
		seen := map[string]bool{h: true}
		frontier := []string{h}
		var out []string
		for depth := 0; depth < 4 && len(frontier) > 0; depth++ {
			var next []string
			for _, n := range frontier {
				out = append(out, writes[n]...)
				for _, c := range calls[n] {
					if !seen[c] {
						seen[c] = true
						next = append(next, c)
					}
				}
			}
			frontier = next
		}
		return out
	}
	fileAuth := map[string]bool{}
	var fs []Finding
	seen := map[string]bool{}
	for _, r := range g.Routes {
		if r.Guards == "" || guarded(r.Guards) || r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" || rePublicPath.MatchString(r.Path) {
			continue
		}
		k := r.Method + " " + r.Path
		if seen[k] {
			continue
		}
		w := reach(r.Handler)
		if len(w) == 0 {
			continue
		}
		if r.HandlerFile != "" {
			ok, done := fileAuth[r.HandlerFile]
			if !done {
				b, _ := os.ReadFile(filepath.Join(root, r.HandlerFile))
				ok = reHandlerAuth.Match(stripComments(b, r.HandlerFile))
				fileAuth[r.HandlerFile] = ok
			}
			if ok {
				continue
			}
		}
		seen[k] = true
		sort.Strings(w)
		w = slicesCompact(w)
		fs = append(fs, Finding{Severity: Medium, Rule: "route-no-auth", Target: k, Heuristic: true,
			Message: "Route writes to the database (" + strings.Join(w[:min(3, len(w))], ", ") + ") and no login guard runs before it: anyone who finds the URL can call it.",
			Where:   r.File + ":" + itoa(r.Line), Fix: "put it behind the login (before_action, auth middleware, pipe_through, [Authorize], Depends(current_user)) or check the user in the handler",
			UsedIn: []string{r.HandlerFile + " " + r.Handler}})
	}
	return fs
}

func slicesCompact(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
