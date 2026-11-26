package cli

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/codumihq/codumi/internal/store"
)

// A minimal MCP server (JSON-RPC 2.0, newline-delimited, over stdio).
// Tools return the same compact text as the CLI, which is cheaper for an
// agent to read than JSON.

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type prop struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema struct {
		Type       string          `json:"type"`
		Properties map[string]prop `json:"properties"`
		Required   []string        `json:"required,omitempty"`
	} `json:"inputSchema"`
	// Meta: o Claude Code carrega as ferramentas MCP sob demanda (ToolSearch);
	// as do Codumi vêm sempre carregadas, para serem a primeira escolha.
	Meta map[string]any `json:"_meta,omitempty"`
	// Annotations diz aos agentes o que a ferramenta faz: todas só leem o índice,
	// menos codumi_index, que o reconstrói (sem apagar nada do projeto). Agentes
	// como o Codex usam isso para não pedir aprovação de uma consulta.
	Annotations map[string]any `json:"annotations,omitempty"`
	run         func(args map[string]any) error
}

func mkTool(name, desc string, props map[string]prop, required []string, run func(map[string]any) error) tool {
	t := tool{Name: name, Description: desc, run: run, Meta: map[string]any{"anthropic/alwaysLoad": true}}
	t.Annotations = map[string]any{"readOnlyHint": name != "codumi_index" && name != "codumi_remember" && name != "codumi_plan" && name != "codumi_room_say", "destructiveHint": false, "idempotentHint": name != "codumi_remember" && name != "codumi_plan", "openWorldHint": false}
	t.InputSchema.Type = "object"
	if props == nil {
		props = map[string]prop{}
	}
	props["project"] = prop{Type: "string", Description: "Project to query: a name from the known projects or an absolute path. Defaults to the project that contains the session's working directory."}
	t.InputSchema.Properties = props
	t.InputSchema.Required = required
	return t
}

func str(args map[string]any, k string) string {
	switch v := args[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func tools() []tool {
	return []tool{
		mkTool("codumi_refs",
			"Every exact use of a function, component, type or Class.member in the project, with file and line, resolved by the TypeScript type checker (no name guessing). Use this instead of grep to find call sites before changing or deleting code.",
			map[string]prop{
				"name":  {Type: "string", Description: "Declaration name, Class.member, or path/file.ts:name to disambiguate"},
				"all":   {Type: "boolean", Description: "Also list import/export lines"},
				"limit": {Type: "number", Description: "Max lines (default 80, 0 = all)"},
			}, []string{"name"},
			func(a map[string]any) error {
				f := map[string]string{}
				if str(a, "all") == "true" {
					f["all"] = "true"
				}
				if l := str(a, "limit"); l != "" {
					f["limit"] = l
				}
				return cmdRefs([]string{str(a, "name")}, f)
			}),
		mkTool("codumi_route",
			"An API route (Next.js app router): its handler, every table/RPC/bucket it reaches through the call graph, and the places in the code that call it.",
			map[string]prop{
				"path":   {Type: "string", Description: "Route path like /api/orders, or part of it"},
				"detail": {Type: "boolean", Description: "Every database use with line and call path"},
			}, []string{"path"},
			func(a map[string]any) error {
				f := map[string]string{}
				if str(a, "detail") == "true" {
					f["detail"] = "true"
				}
				return cmdRoute([]string{str(a, "path")}, f)
			}),
		mkTool("codumi_page",
			"A UI page (Next.js app or pages router): its layouts, the tree of components it renders (JSX, with file and line), and the tables, RPCs and API routes those components use. Use it to find where something on a screen lives before reading files.",
			map[string]prop{
				"path":  {Type: "string", Description: "Page route like / or /orders/[id], part of it, or the page component's name"},
				"depth": {Type: "number", Description: "Levels of components (default 3)"},
				"all":   {Type: "boolean", Description: "Also expand UI primitives (components/ui) and repeated components"},
			}, []string{"path"},
			func(a map[string]any) error {
				f := map[string]string{}
				if d := str(a, "depth"); d != "" {
					f["depth"] = d
				}
				if str(a, "all") == "true" {
					f["all"] = "true"
				}
				return cmdPage([]string{str(a, "path")}, f)
			}),
		mkTool("codumi_find",
			"Find a text (UI copy, a message, a constant) in the project's own source files only, case-insensitive, with the component or function each match is in. Skips node_modules, build output and other folders the index ignores.",
			map[string]prop{
				"text":  {Type: "string", Description: "Text to find"},
				"limit": {Type: "number", Description: "Max matches to list (default 40, 0 = all)"},
			}, []string{"text"},
			func(a map[string]any) error {
				f := map[string]string{}
				if l := str(a, "limit"); l != "" {
					f["limit"] = l
				}
				q := str(a, "text")
				if q == "" {
					q = str(a, "query") // nome que os agentes costumam chutar
				}
				// para o agente, no máximo 100 linhas (limit 0 traria milhares)
				if n, err := strconv.Atoi(f["limit"]); err == nil && (n <= 0 || n > 100) {
					f["limit"] = "100"
				}
				return cmdFind([]string{q}, f)
			}),
		mkTool("codumi_table",
			"A Supabase table, view, RPC function or storage bucket: RLS state, policies, function signature and EXECUTE grants, callers inside the database (functions, triggers, policies), and every place the code uses it (marking service-role clients that bypass RLS).",
			map[string]prop{"name": {Type: "string", Description: "Table, view, function or bucket name"}}, []string{"name"},
			func(a map[string]any) error { return cmdTable([]string{str(a, "name")}, map[string]string{}) }),
		mkTool("codumi_audit",
			"Access-control audit of the Supabase project: tables without RLS, SECURITY DEFINER functions anon can call, listable storage buckets, definer views, webhooks without verification, exposed service role key. Findings include migration/file line and a fix.",
			map[string]prop{"min_severity": {Type: "string", Description: "Only show findings at least this severe", Enum: []string{"high", "medium", "low"}}}, nil,
			func(a map[string]any) error {
				f := map[string]string{}
				if s := str(a, "min_severity"); s != "" {
					f["min"] = s
				}
				return cmdAudit(nil, f)
			}),
		mkTool("codumi_impact",
			"What the current changes affect, before you finish a task: reads the git diff (uncommitted, or since a branch with base), finds the declarations whose lines changed and follows the call graph up: callers, API routes and pages that reach them, tables/RPCs the changed code touches, API routes it calls, schema from changed migrations, and the tests on the path. Use it to know what to test and review.",
			map[string]prop{
				"base":  {Type: "string", Description: "Compare with this branch or commit (e.g. main). Default: uncommitted changes"},
				"depth": {Type: "number", Description: "Levels of callers (default 4)"},
			}, nil,
			func(a map[string]any) error {
				f := map[string]string{}
				if v := str(a, "base"); v != "" {
					f["base"] = v
				}
				if v := str(a, "depth"); v != "" {
					f["depth"] = v
				}
				return cmdImpact(nil, f)
			}),
		mkTool("codumi_error",
			"A production error, followed to the exact code: pass the error text with its stack trace (from logs, Vercel, Supabase, the browser console) or a Sentry issue link. Returns each stack line resolved to the declaration it falls in, the API routes and pages that reach it, what the failing code touches in the database and the tests on the path. Use it before reading files when fixing an error.",
			map[string]prop{
				"trace":  {Type: "string", Description: "The error message and stack trace, as pasted"},
				"sentry": {Type: "string", Description: "A Sentry issue link (needs SENTRY_AUTH_TOKEN)"},
			}, nil,
			func(a map[string]any) error {
				return cmdError(nil, map[string]string{"text": str(a, "trace"), "sentry": str(a, "sentry")})
			}),
		mkTool("codumi_room_say",
			"Only for agents working in a Codumi room (the room instructions give your room and agent ids). Tell another agent in the room, or all of them, something that affects their work: an API or type you changed, a table or column, a shared file, a decision. The user sees it in the room and delivers it. Keep it short and concrete.",
			map[string]prop{
				"room": {Type: "string", Description: "Your room id"},
				"from": {Type: "string", Description: "Your agent id"},
				"to":   {Type: "string", Description: "The other agent's id or area, or all"},
				"text": {Type: "string", Description: "The message"},
			}, []string{"room", "from", "text"},
			func(a map[string]any) error { return roomSay(str(a, "room"), str(a, "from"), str(a, "to"), str(a, "text")) }),
		mkTool("codumi_unused",
			"Dead code: declarations with no real use in the project (imports don't count), leaving out what frameworks call by themselves (pages, route handlers, main, tests) and methods. Internal ones are safe to remove; exported ones may be used by another project.",
			map[string]prop{
				"kind":    {Type: "string", Description: "Only this kind (function, class, component, struct…)"},
				"methods": {Type: "boolean", Description: "Also list methods"},
				"limit":   {Type: "number", Description: "Max lines (default 100, 0 = all)"},
			}, nil,
			func(a map[string]any) error {
				f := map[string]string{}
				for _, k := range []string{"kind", "methods", "limit"} {
					if v := str(a, k); v != "" {
						f[k] = v
					}
				}
				return cmdUnused(nil, f)
			}),
		mkTool("codumi_sql",
			"Read-only SQL over the index (SQLite). Tables: files(path,lines), decls(file,name,kind,line,exported), refs(file,line,from_name,to_file,to_name,kind), db_uses(file,line,from_name,type,name,ops,client), routes(file,method,path,line), route_calls(file,line,from_name,path), sql_tables, sql_policies, sql_functions, buckets.",
			map[string]prop{"query": {Type: "string", Description: "A SELECT statement"}}, []string{"query"},
			func(a map[string]any) error { return cmdSQL([]string{str(a, "query")}, map[string]string{}) }),
		mkTool("codumi_note",
			"Module notes: short human-written cards per feature (decisions, gotchas, pending work) plus live facts from the index (routes, tables, who depends on it). Read the note before changing code in a module. With no arguments, lists the notes.",
			map[string]prop{
				"name": {Type: "string", Description: "Note slug or name"},
				"file": {Type: "string", Description: "A file path: returns the notes that cover it"},
			}, nil,
			func(a map[string]any) error {
				f := map[string]string{}
				if v := str(a, "file"); v != "" {
					f["file"] = v
					return cmdNote(nil, f)
				}
				if v := str(a, "name"); v != "" {
					return cmdNote([]string{v}, f)
				}
				return cmdNotes(nil, f)
			}),
		mkTool("codumi_index",
			"Rebuild the index. Call it after pulling or after large edits, or when a result says the repo changed after the last index.",
			nil, nil,
			func(a map[string]any) error {
				cwd, _ := os.Getwd()
				return cmdIndex([]string{cwd}, map[string]string{})
			}),
		mkTool("codumi_remember",
			"Project memory that lasts between conversations. Save one short sentence when the user states a lasting preference or rule (\"always use pnpm\", \"never touch the migrations folder\"), or when you find a decision or gotcha the next conversation should know. Don't save what the code or git already shows, nor anything secret. Use forget with part of a fact's text to remove one that is wrong or outdated. The memory is sent at the start of each conversation.",
			map[string]prop{
				"fact":   {Type: "string", Description: "The fact to remember, one short sentence in the user's language"},
				"forget": {Type: "string", Description: "Text of a saved fact to remove"},
			}, nil,
			func(a map[string]any) error {
				root, _ := os.Getwd()
				if r, _, ok := store.Find(root); ok {
					root = r
				}
				msg, err := Remember(root, str(a, "fact"), str(a, "forget"))
				if err != nil {
					return err
				}
				fmt.Fprintln(out, msg)
				return nil
			}),
		mkTool("codumi_look",
			"Look at the project's page in a real browser: returns a screenshot and the console messages. By default it opens the preview the user is running in the Codumi app; pass path for another page (\"/login\") or url for any address. Use it after changing UI to check that it looks right, and when the user reports something visual.",
			map[string]prop{
				"path":  {Type: "string", Description: "Page path on the preview, like /login (optional)"},
				"url":   {Type: "string", Description: "Full http(s) address instead of the preview (optional)"},
				"width": {Type: "number", Description: "Viewport width in px: 1280 desktop (default), 390 phone"},
			}, nil,
			func(a map[string]any) error {
				root, _ := os.Getwd()
				if r, _, ok := store.Find(root); ok {
					root = r
				}
				w, _ := strconv.Atoi(str(a, "width"))
				text, img, err := Look(root, str(a, "url"), str(a, "path"), w)
				if err != nil {
					return err
				}
				fmt.Fprint(out, text)
				outImages = append(outImages, img)
				return nil
			}),
		mkTool("codumi_plan",
			"Show the user your checklist for the current task, live in the Codumi app. Call it once you know the steps of a task with 3 or more steps, and again each time a step starts or ends, always sending the whole list. One step per line, prefixed by [ ] to do, [>] doing now (only one), [x] done, [-] dropped. Keep each step short, in the user's language.",
			map[string]prop{
				"steps": {Type: "string", Description: "The whole checklist, one step per line: \"[x] Read the route\\n[>] Fix the query\\n[ ] Run the tests\""},
			}, []string{"steps"},
			func(a map[string]any) error {
				done, total := 0, 0
				for _, l := range strings.Split(str(a, "steps"), "\n") {
					l = strings.TrimSpace(l)
					if l == "" {
						continue
					}
					total++
					if strings.HasPrefix(l, "[x]") || strings.HasPrefix(l, "[X]") {
						done++
					}
				}
				if total == 0 {
					return fmt.Errorf("send at least one step")
				}
				fmt.Fprintf(out, "Checklist shown to the user: %d of %d done.\n", done, total)
				if done < total {
					fmt.Fprintln(out, "As soon as the current step is finished, call codumi_plan again with it marked [x] and the next one [>]. The user follows your progress through this list.")
				}
				return nil
			}),
	}
}

func cmdMCP(pos []string, flags map[string]string) error {
	root := "."
	if len(pos) > 0 {
		root = pos[0]
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if r, _, ok := store.Find(root); ok {
		root = r
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	return serveMCP(os.Stdin, os.Stdout)
}

func serveMCP(in io.Reader, w io.Writer) error {
	ts := tools()
	byName := map[string]tool{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	enc := json.NewEncoder(w)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(rpcResp{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error"}})
			continue
		}
		if len(req.ID) == 0 { // notification
			continue
		}
		resp := rpcResp{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(req.Params, &p)
			pv := p.ProtocolVersion
			if pv == "" {
				pv = "2025-06-18"
			}
			resp.Result = map[string]any{
				"protocolVersion": pv,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "codumi", "version": version},
				"instructions":    "Known projects: " + knownList() + ". If the session's working directory is not inside one of them, pass `project` (name or absolute path) on every call. Codumi has an exact index of this project. Before working in a module, read its note (codumi_note with the file path). Prefer codumi_refs over grep to find call sites, codumi_page to find the components of a screen, codumi_find to find UI text in the source, codumi_route to see what an endpoint touches, codumi_table for RLS/policies/uses of a table, RPC or bucket, and codumi_audit for security review. To fix a production error, pass its stack trace to codumi_error first. Before finishing a change, run codumi_impact to see what it affects and which tests to run; codumi_unused lists dead code.",
			}
		case "ping":
			resp.Result = map[string]any{}
		case "tools/list":
			resp.Result = map[string]any{"tools": ts}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				resp.Error = &rpcErr{-32602, "invalid params"}
				break
			}
			t, ok := byName[p.Name]
			if !ok {
				resp.Error = &rpcErr{-32602, "unknown tool " + p.Name}
				break
			}
			outImages = nil
			text, isErr := runTool(t, p.Arguments)
			content := []map[string]any{{"type": "text", "text": text}}
			for _, img := range outImages {
				content = append(content, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(img), "mimeType": "image/png"})
			}
			outImages = nil
			resp.Result = map[string]any{
				"content": content,
				"isError": isErr,
			}
		default:
			resp.Error = &rpcErr{-32601, "method not found: " + req.Method}
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	pendingUsage.Wait()
	return sc.Err()
}

// runTool runs a command with its output captured. If there is no index
// yet, it builds one first.
func runTool(t tool, args map[string]any) (string, bool) {
	var buf, side bytes.Buffer
	prevOut, prevNotes := out, notes
	out, notes = &buf, &side
	defer func() { out, notes = prevOut, prevNotes }()

	if args == nil {
		args = map[string]any{}
	}
	// Run in the requested project, then come back.
	home, _ := os.Getwd()
	dir, err := resolveProject(str(args, "project"), home)
	if err != nil {
		return err.Error(), true
	}
	if dir != home {
		if err := os.Chdir(dir); err != nil {
			return err.Error(), true
		}
		defer os.Chdir(home)
	}
	// estas não leem o índice: funcionam em qualquer pasta
	noIndex := t.Name == "codumi_look" || t.Name == "codumi_plan" || t.Name == "codumi_room_say"
	if _, _, ok := store.Find(dir); !ok && !noIndex {
		// Only build an index on our own for something that is clearly a
		// project; never for a home or parent folder.
		if !looksLikeProject(dir) {
			return "no Codumi index for " + dir + ". Pass `project` with one of the known projects (" + knownList() + ") or an absolute path to a project root.", true
		}
		if t.Name != "codumi_index" {
			if err := cmdIndex([]string{dir}, map[string]string{}); err != nil {
				return "no index yet, and building it failed: " + err.Error(), true
			}
			buf.WriteString("(built the index first)\n\n")
		}
	}
	start := time.Now()
	err = t.run(args)
	took := time.Since(start)
	text := buf.String()
	if n := strings.TrimSpace(side.String()); n != "" && t.Name != "codumi_index" {
		text = n + "\n\n" + text
	}
	if err != nil {
		if e, ok := err.(exitErr); ok && e == 2 {
			return text, false
		}
		return strings.TrimSpace(text + "\n" + err.Error()), true
	}
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}
	// resposta grande demais o agente não consegue ler de uma vez: corta com aviso
	const maxOut = 30000
	if len(text) > maxOut {
		cut := strings.LastIndexByte(text[:maxOut], '\n')
		if cut < 0 {
			cut = maxOut
		}
		rest := strings.Count(text[cut:], "\n")
		text = text[:cut] + fmt.Sprintf("\n… cut here, %d more lines. Ask a narrower question: a more specific text, a route with codumi_page, or a name with codumi_refs.\n", rest)
	}
	if t.Name != "codumi_index" && os.Getenv("CODUMI_NO_USAGE") == "" {
		root := dir
		if r, _, ok := store.Find(dir); ok {
			root = r
		}
		n := len(text)
		pendingUsage.Add(1)
		go func() {
			defer pendingUsage.Done()
			logUsage(root, t.Name, args, n, took)
		}()
	}
	return text, false
}

// KnownProjects lists the project folders registered by the desktop app or
// by `codumi index` (projects.json in the user config dir).
func KnownProjects() []string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	dir = store.UserDir(dir)
	if v := os.Getenv("CODUMI_CONFIG_DIR"); v != "" {
		dir = v
	}
	var ps []string
	if b, err := os.ReadFile(filepath.Join(dir, "projects.json")); err == nil {
		json.Unmarshal(b, &ps)
	}
	return ps
}

func knownList() string {
	var names []string
	for _, p := range KnownProjects() {
		if _, _, ok := store.Find(p); ok {
			names = append(names, filepath.Base(p))
		}
	}
	if len(names) == 0 {
		return "none yet"
	}
	return strings.Join(names, ", ")
}

// resolveProject turns the `project` argument into a directory. Empty means
// the working directory.
func resolveProject(arg, cwd string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return cwd, nil
	}
	if strings.HasPrefix(arg, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			arg = filepath.Join(h, arg[1:])
		}
	}
	if filepath.IsAbs(arg) {
		if st, err := os.Stat(arg); err == nil && st.IsDir() {
			return arg, nil
		}
		return "", fmt.Errorf("project path %s does not exist", arg)
	}
	var hit []string
	for _, p := range KnownProjects() {
		if strings.EqualFold(filepath.Base(p), arg) {
			hit = append(hit, p)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], nil
	case 0:
		return "", fmt.Errorf("unknown project %q. Known projects: %s. An absolute path also works.", arg, knownList())
	}
	return "", fmt.Errorf("project %q is ambiguous (%s); pass the absolute path", arg, strings.Join(hit, ", "))
}

func looksLikeProject(dir string) bool {
	if h, err := os.UserHomeDir(); err == nil && filepath.Clean(dir) == filepath.Clean(h) {
		return false
	}
	for _, f := range []string{".git", "package.json", "tsconfig.json", "go.mod", "pubspec.yaml", "pyproject.toml", "requirements.txt", "supabase"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}
