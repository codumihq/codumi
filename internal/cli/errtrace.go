package cli

// codumi erro: um erro de produção (a pilha colada, ou um problema do Sentry)
// vira o caminho exato no mapa: em que declaração cada linha da pilha cai, e
// daí para cima as rotas e telas que chegam nela, o que ela toca no banco e os
// testes no caminho.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type errFrame struct {
	Path string `json:"path"` // como veio na pilha
	Line int    `json:"line"`
	Fn   string `json:"fn,omitempty"`
	// preenchidos ao achar no mapa
	File string `json:"file,omitempty"`
	Decl string `json:"decl,omitempty"`
}

var (
	// at fn (path:12:5) | at path:12:5 | fn@path:12:5
	reJS = regexp.MustCompile(`(?:at\s+(?:async\s+)?(?:(\S+?)\s+\()?|(\S+)@)((?:[A-Za-z][\w-]*:/{0,3})?[^\s():]+\.[A-Za-z0-9]+):(\d+)(?::\d+)?\)?`)
	// File "path", line 12, in fn
	rePy = regexp.MustCompile(`File "([^"]+)", line (\d+)(?:, in (\S+))?`)
	// path.go:12 +0x… (Go), path.rb:12:in `fn' (Ruby), path.ex:12: (Elixir)
	reColon = regexp.MustCompile(`(?:^|\s|\()((?:[A-Za-z]:)?[^\s():'"]+\.(?:go|rb|ex|exs|rs|swift|scala|cs|php|dart|kt|java|py|ts|tsx|js|jsx|mjs|cjs|vue)):(\d+)(?::\d+)?(?:[:\s)]|$)`)
	// at pkg.Class.method(File.kt:12)
	reJVM = regexp.MustCompile(`at\s+([\w$.<>]+)\(([\w$-]+\.(?:java|kt|scala)):(\d+)\)`)
	// #0 /path/x.php(12): fn()
	rePHP = regexp.MustCompile(`#\d+\s+((?:[A-Za-z]:)?[^\s(]+\.php)\((\d+)\)(?::\s*(\S+?)\()?`)
	// in /path/x.cs:line 12
	reCS = regexp.MustCompile(`at\s+([\w.<>` + "`" + `]+)\(.*?\)\s+in\s+(.+?\.cs):line (\d+)`)
	// #0 fn (package:app/x.dart:12:5)
	reDart = regexp.MustCompile(`#\d+\s+(\S+)\s+\((?:package:[^/]+/|file://)?([^\s():]+\.dart):(\d+)(?::\d+)?\)`)
)

// parseFrames tira da pilha (de qualquer linguagem comum) os pares arquivo:linha, na ordem.
func parseFrames(text string) (title string, frames []errFrame) {
	seen := map[string]bool{}
	add := func(p, line, fn string) {
		n, _ := strconv.Atoi(line)
		p = strings.TrimSpace(p)
		if n <= 0 || p == "" {
			return
		}
		for _, pre := range []string{"webpack-internal:///", "webpack:///", "file://", "app:///", "rsc://React/Server/"} {
			p = strings.TrimPrefix(p, pre)
		}
		if i := strings.Index(p, "?"); i > 0 {
			p = p[:i]
		}
		k := p + ":" + line
		if seen[k] {
			return
		}
		seen[k] = true
		frames = append(frames, errFrame{Path: p, Line: n, Fn: strings.Trim(fn, "`'\"")})
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		l := sc.Text()
		t := strings.TrimSpace(l)
		if title == "" && t != "" && !strings.HasPrefix(t, "at ") && !strings.HasPrefix(t, "File \"") && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "Traceback") && !strings.HasPrefix(t, "goroutine ") {
			title = t
		}
		switch {
		case reDart.MatchString(l):
			m := reDart.FindStringSubmatch(l)
			add(m[2], m[3], m[1])
		case rePy.MatchString(l):
			m := rePy.FindStringSubmatch(l)
			add(m[1], m[2], m[3])
		case reJVM.MatchString(l):
			m := reJVM.FindStringSubmatch(l)
			fn := m[1]
			if i := strings.LastIndex(fn, "."); i > 0 {
				fn = fn[i+1:]
			}
			add(m[2], m[3], fn)
		case reCS.MatchString(l):
			m := reCS.FindStringSubmatch(l)
			add(m[2], m[3], m[1])
		case rePHP.MatchString(l):
			m := rePHP.FindStringSubmatch(l)
			add(m[1], m[2], m[3])
		case reJS.MatchString(l):
			for _, m := range reJS.FindAllStringSubmatch(l, -1) {
				fn := m[1]
				if fn == "" {
					fn = m[2]
				}
				add(m[3], m[4], fn)
			}
		case reColon.MatchString(l):
			m := reColon.FindStringSubmatch(l)
			add(m[1], m[2], "")
		}
	}
	if len(title) > 300 {
		title = title[:300] + "…"
	}
	return title, frames
}

// resolveFile acha o arquivo do índice que corresponde ao caminho da pilha
// (pelo maior pedaço final igual: /var/task/src/lib/x.ts → src/lib/x.ts).
func resolveFile(p string, files []string, byBase map[string][]string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.Contains(p, "node_modules/") || strings.Contains(p, "/site-packages/") || strings.Contains(p, "/go/pkg/mod/") || strings.HasPrefix(p, "node:") || strings.HasPrefix(p, "internal/") {
		return ""
	}
	cands := byBase[path.Base(p)]
	best, bestN := "", 0
	ps := strings.Split(strings.TrimPrefix(p, "./"), "/")
	for _, c := range cands {
		cs := strings.Split(c, "/")
		n := 0
		for n < len(cs) && n < len(ps) && cs[len(cs)-1-n] == ps[len(ps)-1-n] {
			n++
		}
		if n > bestN || (n == bestN && best != "" && len(c) < len(best)) {
			best, bestN = c, n
		}
	}
	// só o nome do arquivo bate: vale se ele for único no projeto
	if bestN == 1 && len(cands) > 1 {
		return ""
	}
	return best
}

// nextRoute: o JavaScript já compilado do Next (.next/server/app/api/x/route.js) volta para a rota.
var reNextBuilt = regexp.MustCompile(`\.next/server/(?:app|pages)(/.*?)/(?:route|page)\.js`)

type errReport struct {
	Title    string     `json:"title"`
	Frames   []errFrame `json:"frames"`
	Outside  int        `json:"outside"` // linhas fora do projeto (bibliotecas, runtime)
	Built    []string   `json:"built,omitempty"`
	Routes   []errHit   `json:"routes"`
	Pages    []errHit   `json:"pages"`
	DB       []string   `json:"db"`
	Tests    []string   `json:"tests"`
	Source   string     `json:"source,omitempty"` // link do Sentry
	NotFound bool       `json:"notFound,omitempty"`
}

type errHit struct {
	Label string `json:"label"`
	Via   string `json:"via"`
}

func cmdError(pos []string, flags map[string]string) error {
	text := flags["text"]
	source := ""
	switch {
	case flags["sentry"] != "":
		tok := flags["token"]
		if tok == "" {
			tok = os.Getenv("SENTRY_AUTH_TOKEN")
		}
		t, link, err := sentryEvent(flags["sentry"], tok)
		if err != nil {
			return err
		}
		text, source = t, link
	case flags["file"] != "":
		b, err := os.ReadFile(flags["file"])
		if err != nil {
			return err
		}
		text = string(b)
	case text == "" && (len(pos) == 0 || pos[0] == "-"):
		b, _ := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
		text = string(b)
	case text == "":
		text = strings.Join(pos, " ")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("paste the error with its stack trace (codumi erro < erro.txt), or pass --sentry=<issue link>")
	}
	cmdIndex([]string{"."}, map[string]string{"quiet": "true"})
	root, db, err := openIndex()
	if err != nil {
		return err
	}
	defer db.Close()
	_ = root
	rep := errReport{Source: source, Routes: []errHit{}, Pages: []errHit{}, DB: []string{}, Tests: []string{}}
	var frames []errFrame
	rep.Title, frames = parseFrames(text)

	var files []string
	byBase := map[string][]string{}
	if rows, err := db.Query(`SELECT path FROM files`); err == nil {
		for rows.Next() {
			var f string
			rows.Scan(&f)
			files = append(files, f)
			byBase[path.Base(f)] = append(byBase[path.Base(f)], f)
		}
		rows.Close()
	}
	starts := []node{}
	startSeen := map[node]bool{}
	for _, f := range frames {
		if m := reNextBuilt.FindStringSubmatch(f.Path); m != nil {
			r := regexp.MustCompile(`/\([^/]*\)`).ReplaceAllString(m[1], "") // grupos (marketing)
			rep.Built = append(rep.Built, r)
			continue
		}
		file := resolveFile(f.Path, files, byBase)
		if file == "" {
			rep.Outside++
			continue
		}
		f.File = file
		for _, d := range declSpans(db, file) {
			if f.Line >= d.from && f.Line <= d.to {
				f.Decl = d.name // a mais interna que contém a linha (membros vêm depois)
			}
		}
		rep.Frames = append(rep.Frames, f)
		if f.Decl != "" {
			n := node{file, f.Decl}
			if !startSeen[n] {
				startSeen[n] = true
				starts = append(starts, n)
			}
		}
	}
	// pilha do Next compilado: a rota/tela entra pelo caminho
	for _, r := range rep.Built {
		if rows, err := db.Query(`SELECT method, path, handler_file, handler FROM routes WHERE path = ?`, r); err == nil {
			for rows.Next() {
				var m, p, hf, h string
				rows.Scan(&m, &p, &hf, &h)
				n := node{hf, h}
				if !startSeen[n] {
					startSeen[n] = true
					starts = append(starts, n)
				}
			}
			rows.Close()
		}
	}

	// sobe o grafo a partir de cada linha da pilha
	type hop struct {
		via   node
		level int
	}
	seen := map[node]hop{}
	for _, n := range starts {
		seen[n] = hop{}
	}
	frontier := starts
	for lvl := 1; lvl <= intFlag(flags, "depth", 4) && len(frontier) > 0; lvl++ {
		var next []node
		for _, n := range frontier {
			rows, err := db.Query(`SELECT DISTINCT file, from_name FROM refs WHERE to_file = ? AND to_name = ? AND kind NOT IN ('import','export')`, n.file, n.name)
			if err != nil {
				continue
			}
			for rows.Next() {
				var c node
				rows.Scan(&c.file, &c.name)
				if c.name == "" {
					c.name = "<module>"
				}
				if _, ok := seen[c]; !ok {
					seen[c] = hop{via: n, level: lvl}
					next = append(next, c)
				}
			}
			rows.Close()
		}
		frontier = next
	}
	pathTo := func(n node) string {
		p := []string{n.name}
		for h := seen[n]; h.level > 0; h = seen[h.via] {
			p = append(p, h.via.name)
		}
		return strings.Join(p, " → ")
	}
	if rows, err := db.Query(`SELECT method, path, handler_file, handler FROM routes`); err == nil {
		for rows.Next() {
			var m, p, hf, h string
			rows.Scan(&m, &p, &hf, &h)
			if _, ok := seen[node{hf, h}]; ok {
				rep.Routes = append(rep.Routes, errHit{strings.TrimSpace(m + " " + p), pathTo(node{hf, h})})
			}
		}
		rows.Close()
	}
	if rows, err := db.Query(`SELECT path, file, component FROM pages`); err == nil {
		for rows.Next() {
			var p, f, c string
			rows.Scan(&p, &f, &c)
			if _, ok := seen[node{f, c}]; ok {
				rep.Pages = append(rep.Pages, errHit{p, pathTo(node{f, c})})
			}
		}
		rows.Close()
	}
	dbs := map[string]map[string]bool{}
	for _, n := range starts {
		if rows, err := db.Query(`SELECT type, name, ops FROM db_uses WHERE file = ? AND from_name = ?`, n.file, n.name); err == nil {
			for rows.Next() {
				var t, nm, ops string
				rows.Scan(&t, &nm, &ops)
				k := t + " " + nm
				if dbs[k] == nil {
					dbs[k] = map[string]bool{}
				}
				for _, o := range strings.Split(ops, ",") {
					if o = strings.TrimSpace(o); o != "" {
						dbs[k][o] = true
					}
				}
			}
			rows.Close()
		}
	}
	for k, ops := range dbs {
		var os []string
		for o := range ops {
			os = append(os, o)
		}
		sort.Strings(os)
		rep.DB = append(rep.DB, strings.TrimSpace(k+" "+strings.Join(os, ",")))
	}
	sort.Strings(rep.DB)
	ts := map[string]bool{}
	for n := range seen {
		if isTestFile(n.file) {
			ts[n.file] = true
		}
	}
	for t := range ts {
		rep.Tests = append(rep.Tests, t)
	}
	sort.Strings(rep.Tests)
	rep.NotFound = len(rep.Frames) == 0 && len(rep.Built) == 0

	if flags["json"] != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	printErrReport(rep)
	return nil
}

func printErrReport(rep errReport) {
	if rep.Title != "" {
		fmt.Fprintln(out, "error:", rep.Title)
	}
	if rep.Source != "" {
		fmt.Fprintln(out, "source:", rep.Source)
	}
	if rep.NotFound {
		fmt.Fprintln(out, "\nno line of the stack trace is in this project's map (minified build? run it with source maps, or paste the server log)")
		return
	}
	if len(rep.Frames) > 0 {
		fmt.Fprintln(out, "\nwhere it broke (innermost first):")
		for i, f := range rep.Frames {
			in := ""
			if f.Decl != "" {
				in = "  in " + f.Decl
			}
			fmt.Fprintf(out, "  %d. %s:%d%s\n", i+1, f.File, f.Line, in)
		}
	}
	if len(rep.Built) > 0 {
		fmt.Fprintf(out, "\nbuilt Next.js files point to: %s\n", strings.Join(rep.Built, ", "))
	}
	if rep.Outside > 0 {
		fmt.Fprintf(out, "  (%d lines outside the project: libraries and runtime)\n", rep.Outside)
	}
	if len(rep.Routes) > 0 {
		fmt.Fprintln(out, "\nAPI routes that reach it:")
		for _, r := range rep.Routes {
			fmt.Fprintf(out, "  %-36s via %s\n", r.Label, r.Via)
		}
	}
	if len(rep.Pages) > 0 {
		fmt.Fprintln(out, "\npages that reach it:")
		for _, p := range rep.Pages {
			fmt.Fprintf(out, "  %-36s via %s\n", p.Label, p.Via)
		}
	}
	if len(rep.DB) > 0 {
		fmt.Fprintln(out, "\ndatabase the failing code touches:")
		for _, d := range rep.DB {
			fmt.Fprintln(out, "  "+d)
		}
	}
	if len(rep.Tests) > 0 {
		fmt.Fprintln(out, "\ntests on the path:")
		for _, t := range rep.Tests {
			fmt.Fprintln(out, "  "+t)
		}
	} else if len(rep.Frames) > 0 {
		fmt.Fprintln(out, "\nno test reaches the failing code: write one that reproduces the error first")
	}
	if len(rep.Frames) > 0 {
		f := rep.Frames[0]
		fmt.Fprintf(out, "\nnext: read %s around line %d (codumi_note on the file for its decisions)\n", f.File, f.Line)
	}
}

// ---- Sentry ------------------------------------------------------------------------

var reSentryIssue = regexp.MustCompile(`^https?://([^/]+)/(?:organizations/([^/]+)/)?issues/(\d+)`)

// sentryAPI: a base da API para o endereço do problema (sentry.io, região ou servidor próprio).
func sentryAPI(host string) string {
	if strings.HasSuffix(host, ".sentry.io") || host == "sentry.io" {
		if strings.HasPrefix(host, "de.") || strings.Contains(host, ".de.") {
			return "https://de.sentry.io"
		}
		return "https://sentry.io"
	}
	return "https://" + host
}

func sentryGet(u, token string, v any) error {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	c := &http.Client{Timeout: 20 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("sentry %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(v)
}

// sentryEvent busca o último evento de um problema e devolve a pilha como texto.
func sentryEvent(link, token string) (string, string, error) {
	if token == "" {
		return "", "", errors.New("set SENTRY_AUTH_TOKEN (a Sentry auth token with event:read)")
	}
	m := reSentryIssue.FindStringSubmatch(strings.TrimSpace(link))
	if m == nil {
		return "", "", errors.New("not a Sentry issue link (…/issues/<id>)")
	}
	host, org, id := m[1], m[2], m[3]
	if org == "" && strings.HasSuffix(host, ".sentry.io") {
		org = strings.Split(host, ".")[0]
	}
	api := sentryAPI(host) + "/api/0/issues/" + id + "/events/latest/"
	if org != "" {
		api = sentryAPI(host) + "/api/0/organizations/" + url.PathEscape(org) + "/issues/" + id + "/events/latest/"
	}
	var ev struct {
		Title   string `json:"title"`
		Entries []struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"entries"`
	}
	if err := sentryGet(api, token, &ev); err != nil {
		return "", "", err
	}
	var sb strings.Builder
	sb.WriteString(ev.Title + "\n")
	for _, e := range ev.Entries {
		if e.Type != "exception" {
			continue
		}
		var d struct {
			Values []struct {
				Type       string `json:"type"`
				Value      string `json:"value"`
				Stacktrace struct {
					Frames []struct {
						Filename string `json:"filename"`
						AbsPath  string `json:"absPath"`
						LineNo   int    `json:"lineNo"`
						Function string `json:"function"`
					} `json:"frames"`
				} `json:"stacktrace"`
			} `json:"values"`
		}
		json.Unmarshal(e.Data, &d)
		for _, v := range d.Values {
			// o Sentry guarda do mais externo para o mais interno: inverte
			fr := v.Stacktrace.Frames
			for i := len(fr) - 1; i >= 0; i-- {
				p := fr[i].Filename
				if p == "" {
					p = fr[i].AbsPath
				}
				if p != "" && fr[i].LineNo > 0 {
					fmt.Fprintf(&sb, "    at %s (%s:%d)\n", orAnon(fr[i].Function), p, fr[i].LineNo)
				}
			}
		}
	}
	return sb.String(), link, nil
}

func orAnon(s string) string {
	if s == "" {
		return "<anonymous>"
	}
	return s
}

// SentryIssue: um problema aberto, para a lista do app.
type SentryIssue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Culprit   string `json:"culprit"`
	Count     string `json:"count"`
	LastSeen  string `json:"lastSeen"`
	Permalink string `json:"permalink"`
}

// SentryIssues lista os problemas abertos de um projeto do Sentry (os mais recentes primeiro).
func SentryIssues(base, org, project, token string) ([]SentryIssue, error) {
	if base == "" {
		base = "https://sentry.io"
	}
	var out []SentryIssue
	u := strings.TrimRight(base, "/") + "/api/0/projects/" + url.PathEscape(org) + "/" + url.PathEscape(project) + "/issues/?query=is:unresolved&statsPeriod=14d&sort=date&limit=25"
	err := sentryGet(u, token, &out)
	return out, err
}
