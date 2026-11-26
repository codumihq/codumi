// Package cli implements the codumi commands. The codumi binary and the
// desktop app both run it.
package cli

import (
	"fmt"
	"os"
	"strings"
)

// Version is the codumi release.
const Version = "0.1.0"

const version = Version

const usageText = `codumi — the exact map of your codebase for AI coding agents

Usage:
  codumi index [path]        build the index (.codumi/codumi.db) for a project
  codumi refs <name>         every use of a function, component, type or Class.member
  codumi table <name>        a table, view, RPC function or bucket: RLS, policies, grants and code uses
  codumi route <path>        an API route: handler, what it touches in the database, who calls it
  codumi page <path>         a UI page: layouts, component tree, what it touches in the database and API
  codumi find "<text>"       a text in the source files, with the component or function it is in
  codumi audit               access-control holes in a Supabase project
  codumi impact              what your uncommitted changes affect: callers, routes, pages, tables, tests
  codumi pr --base=main       pull request review in Markdown: impact + audit findings in the changed files
  codumi banco --db=URL --rls the real database × the migrations, and what anon / a random user can read (read-only)
  codumi erro < erro.txt     a production error (stack trace or --sentry=<issue link>) followed to the code: lines, routes, pages, tables
  codumi unused              declarations nobody uses (dead code)
  codumi sql "<query>"       run a read-only SQL query on the index
  codumi notes               list module notes (.codumi/notes/*.md, committed with the code)
  codumi note <slug>         a module note plus live facts: routes, tables, who uses it, what it uses
  codumi note --file=<path>  the notes that cover a file
  codumi note new <slug> --paths=a/**,b/**   create a note
  codumi link <path>         link another indexed project (route callers across repos)
  codumi stats               tokens saved by agents using Codumi (from the local usage log)
  codumi mcp [path]          MCP server over stdio (for Claude Code, Cursor, Codex…)
  codumi hook install        re-index in the background after commit, checkout and merge
  codumi hook uninstall      remove those git hooks
  codumi version

Aliases (pt-BR): impacto = impact, morto = unused, usa = refs, tabela = table, rota = route, auditoria = audit, fichas = notes, ficha = note, economia = stats, pagina = page, buscar = find.

Flags:
  --json                     machine-readable output (refs, table, route, audit)
  --all                      refs: include import/export lines
  --limit=N                  refs: max lines to print (default 80, 0 = all)
  --detail                   route: up to 3 sites per table/rpc/bucket, with the call path
  --min=high|medium|low      audit: only show findings at least this severe
  --fail-on=high|medium|low  audit: exit with status 2 if a finding is at least this severe
  --full                     index: rebuild everything instead of only what changed
  --quiet                    index: print nothing (used by the git hooks)
  --base=REF                 impact: compare with a branch or commit (default: uncommitted changes)
  --depth=N                  impact: levels of callers to follow (default 4)
  --methods                  unused: also list methods
  --kind=K                   unused: only one kind (function, class, component…)
`

// Main runs a codumi command (args without the program name) and returns
// the process exit code.
func Main(args []string) int {
	if len(args) < 1 {
		fmt.Print(usageText)
		return 1
	}
	cmd := args[0]
	pos, flags := parseArgs(args[1:])
	var err error
	switch cmd {
	case "index", "indexar":
		err = cmdIndex(pos, flags)
	case "refs", "usa", "uses":
		err = cmdRefs(pos, flags)
	case "table", "tabela", "db":
		err = cmdTable(pos, flags)
	case "page", "pagina", "página":
		err = cmdPage(pos, flags)
	case "find", "buscar":
		err = cmdFind(pos, flags)
	case "route", "rota":
		err = cmdRoute(pos, flags)
	case "audit", "auditoria":
		err = cmdAudit(pos, flags)
	case "sql":
		err = cmdSQL(pos, flags)
	case "impact", "impacto":
		err = cmdImpact(pos, flags)
	case "error", "erro":
		err = cmdError(pos, flags)
	case "pr":
		err = cmdPR(pos, flags)
	case "banco", "drift":
		err = cmdDB(pos, flags)
	case "unused", "morto", "sem-uso":
		err = cmdUnused(pos, flags)
	case "overview", "retrato":
		err = cmdOverview(pos, flags)
	case "notes", "fichas":
		err = cmdNotes(pos, flags)
	case "note", "ficha":
		err = cmdNote(pos, flags)
	case "link", "ligar":
		err = cmdLink(pos, flags)
	case "stats", "economia":
		err = cmdStats(pos, flags)
	case "mcp":
		err = cmdMCP(pos, flags)
	case "guard":
		err = cmdGuard(pos, flags)
	case "hook", "hooks":
		err = cmdHook(pos, flags)
	case "version", "--version", "-v":
		fmt.Println("codumi", version)
	case "help", "--help", "-h":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "codumi: unknown command %q\n\n%s", cmd, usageText)
		return 1
	}
	if err != nil {
		if e, ok := err.(exitErr); ok {
			return int(e)
		}
		fmt.Fprintln(os.Stderr, "codumi:", err)
		return 1
	}
	return 0
}

type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func parseArgs(args []string) (pos []string, flags map[string]string) {
	flags = map[string]string{}
	for _, a := range args {
		if strings.HasPrefix(a, "--") && len(a) > 2 {
			k, v, ok := strings.Cut(a[2:], "=")
			if !ok {
				v = "true"
			}
			flags[k] = v
			continue
		}
		pos = append(pos, a)
	}
	return pos, flags
}
