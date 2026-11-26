package cli

// codumi guard: ganchos do Claude Code que fazem o agente passar pelo Codumi.
// A cada pergunta nova ("prompt" zera), a primeira olhada no código de um
// projeto mapeado tem de ser pelo Codumi. Depois, a liberação é por arquivo:
// o Read passa direto nos arquivos que o Codumi apontou (ou que já foram
// lidos nesta pergunta); num arquivo novo, a primeira tentativa volta com a
// sugestão de perguntar ao Codumi, e a segunda passa. Um grep (Grep, grep,
// rg, git grep) por um nome que existe no mapa volta com a resposta do
// codumi refs junto: o agente já recebe quem usa, com arquivo e linha; se
// repetir a mesma busca, ela passa. Busca por texto qualquer, Glob, build,
// git, npm e afins nunca são bloqueados, nem filtros depois de um |.
// "post" anota cada consulta ao Codumi e os arquivos que ela apontou.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/codumihq/codumi/internal/store"
)

type hookInput struct {
	SessionID    string          `json:"session_id"`
	ToolName     string          `json:"tool_name"`
	ToolInput    map[string]any  `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Cwd          string          `json:"cwd"`
}

// guardState: o que já aconteceu nesta pergunta.
type guardState struct {
	At     int64           `json:"at"`     // última consulta ao Codumi (0 = nenhuma)
	Files  map[string]bool `json:"files"`  // arquivos que o Codumi apontou ou que já foram lidos
	Warned map[string]bool `json:"warned"` // arquivos e buscas que já voltaram uma vez
}

func loadGuard(p string) *guardState {
	g := &guardState{}
	if b, err := os.ReadFile(p); err == nil {
		if json.Unmarshal(b, g) != nil {
			// formato antigo: só a hora da consulta
			if ts, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
				g.At = ts
			}
		}
	}
	if g.Files == nil {
		g.Files = map[string]bool{}
	}
	if g.Warned == nil {
		g.Warned = map[string]bool{}
	}
	if g.At != 0 && time.Since(time.Unix(g.At, 0)) > guardWindow {
		g.At = 0
	}
	return g
}

// lockGuard trava o estado da sessão: o agente pede várias ferramentas de
// uma vez e os ganchos rodam ao mesmo tempo (sem a trava, um apaga o outro).
func lockGuard(p string) func() {
	os.MkdirAll(filepath.Dir(p), 0o700)
	l := p + ".lock"
	for i := 0; i < 200; i++ {
		f, err := os.OpenFile(l, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(l) }
		}
		// trava esquecida por um gancho que morreu
		if st, err := os.Stat(l); err == nil && time.Since(st.ModTime()) > 5*time.Second {
			os.Remove(l)
			continue
		}
		time.Sleep(15 * time.Millisecond)
	}
	return func() {}
}

func (g *guardState) save(p string) {
	os.MkdirAll(filepath.Dir(p), 0o700)
	b, _ := json.Marshal(g)
	os.WriteFile(p, b, 0o600)
}

var (
	// caminho de arquivo de código numa resposta do Codumi
	rePathInText = regexp.MustCompile(`(?:[\w.@\[\]()-]+/)*[\w.@\[\]()-]+\.(?:tsx?|jsx?|mjs|cjs|go|py|rb|exs?|dart|sql|vue|svelte|php|rs|java|kt|swift|cs|css|scss|html)\b`)
	// nome de função, componente ou tipo (o que o codumi refs responde)
	reIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{2,}(\.[A-Za-z_$][A-Za-z0-9_$]*)?$`)
	// comando que mostra o conteúdo de um arquivo no terminal
	reFileCat = regexp.MustCompile(`(^|;|&&|\|\||\()\s*(cat|sed|head|tail|less|more|nl|bat)\s`)
	// extensões que não são código: config, docs, dados
	nonCode = map[string]bool{".json": true, ".md": true, ".yaml": true, ".yml": true, ".toml": true, ".lock": true, ".env": true, ".txt": true, ".csv": true, ".svg": true, ".png": true, ".jpg": true, ".ini": true, ".xml": true, ".plist": true, ".gitignore": true, ".example": true}
)

// relIn devolve o caminho do arquivo relativo ao projeto ("" se fora dele).
func relIn(root, cwd, p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	r, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(r, "..") {
		return ""
	}
	return filepath.ToSlash(r)
}

// grepPattern acha o padrão buscado num Grep ou num comando grep/rg/git grep.
func grepPattern(tool string, in map[string]any) string {
	if tool == "Grep" {
		s, _ := in["pattern"].(string)
		return s
	}
	if tool != "Bash" {
		return ""
	}
	c, _ := in["command"].(string)
	m := regexp.MustCompile(`(?:^|;|&&|\|\||\()\s*(?:e?grep|rg|ag|ack|git\s+grep)\s+((?:-\S+\s+)*)(?:'([^']*)'|"([^"]*)"|(\S+))`).FindStringSubmatch(c)
	if m == nil {
		return ""
	}
	for _, x := range m[2:] {
		if x != "" {
			return x
		}
	}
	return ""
}

// identOf: o nome de função/tipo de uma busca, se ela for por um nome só
// (com ou sem \b, \( ou "function " em volta).
func identOf(pat string) string {
	p := strings.TrimSpace(pat)
	for _, pre := range []string{"\\b", "\\<", "function ", "const ", "class ", "type ", "interface ", "func ", "def ", "export "} {
		p = strings.TrimPrefix(p, pre)
	}
	for _, suf := range []string{"\\b", "\\>", "\\(", "(", "\\s*\\(", "\\s*="} {
		p = strings.TrimSuffix(p, suf)
	}
	if reIdent.MatchString(p) {
		return p
	}
	return ""
}

// firstTarget: o que a ferramenta lê (arquivo, padrão ou comando), para
// reconhecer a mesma tentativa repetida.
func firstTarget(in map[string]any) string {
	for _, k := range []string{"file_path", "pattern", "command", "path"} {
		if v, _ := in[k].(string); v != "" {
			return v
		}
	}
	return ""
}

// refsHint roda o codumi refs no projeto e devolve a resposta (curta), ou ""
// se o nome não está no mapa.
func refsHint(root, name string) string {
	o, _, code := Run(root, "refs", name, "--limit=40")
	o = strings.TrimSpace(o)
	if code != 0 || o == "" || strings.Contains(o, "no declaration") || strings.Contains(o, "not found") || strings.HasPrefix(o, "0 ") {
		return ""
	}
	if len(o) > 6000 {
		o = o[:6000] + "\n…"
	}
	return o
}

var (
	// comando que lê ou varre código, no começo de um comando (não depois de um |)
	codeReader = regexp.MustCompile(`(^|;|&&|\|\||\(|\bthen\b|\bdo\b|\bxargs\b)\s*(e?grep|rg|ag|ack|git\s+grep|find|sed|awk|cat|head|tail|less|more|nl|wc)\s`)
)

const guardWindow = 30 * time.Minute // segurança, caso o aviso de pergunta nova não chegue

func guardFile(session string) string {
	session = regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(session, "")
	if session == "" {
		session = "default"
	}
	return filepath.Join(os.TempDir(), "codumi-guard", session)
}

// isCodeRead diz se a chamada olha o código do projeto em root.
func isCodeRead(tool string, in map[string]any, root string) bool {
	switch tool {
	case "Grep", "Glob":
		return true
	case "Read":
		p, _ := in["file_path"].(string)
		if p == "" {
			return false
		}
		if !filepath.IsAbs(p) {
			return true
		}
		r, err := filepath.Rel(root, p)
		return err == nil && !strings.HasPrefix(r, "..")
	case "Bash":
		c, _ := in["command"].(string)
		// tira o que está entre aspas para "grep" dentro de um texto não contar
		c = regexp.MustCompile(`'[^']*'|"[^"]*"`).ReplaceAllString(c, `""`)
		return codeReader.MatchString(c)
	}
	return false
}

func cmdGuard(pos []string, flags map[string]string) error {
	var in hookInput
	b, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if json.Unmarshal(b, &in) != nil {
		return nil // não entendeu: não atrapalha o agente
	}
	mode := ""
	if len(pos) > 0 {
		mode = pos[0]
	}
	p := guardFile(in.SessionID)
	if lf := os.Getenv("CODUMI_GUARD_LOG"); lf != "" {
		if f, err := os.OpenFile(lf, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			f.WriteString(time.Now().Format("15:04:05.000") + " " + mode + " " + in.ToolName + " " + in.SessionID + "\n")
			f.Close()
		}
	}
	switch mode {
	case "snap":
		// cópia do arquivo antes de o agente editar (o app mostra e desfaz)
		dir := in.Cwd
		if dir == "" {
			dir, _ = os.Getwd()
		}
		for _, k := range []string{"file_path", "notebook_path"} {
			if f, _ := in.ToolInput[k].(string); f != "" {
				if !filepath.IsAbs(f) {
					f = filepath.Join(dir, f)
				}
				now := time.Now().Unix()
				SnapTake(flags["dir"], f, now)
				// e a cópia da mensagem atual, para voltar a conversa com os arquivos
				if t, err := os.ReadFile(filepath.Join(flags["dir"], "turn")); err == nil {
					if turn := snapSafe(strings.TrimSpace(string(t))); turn != "" {
						SnapTake(filepath.Join(flags["dir"], "turns", turn), f, now)
					}
				}
			}
		}
		return nil
	case "prompt":
		os.Remove(p) // pergunta nova: a primeira olhada volta a ser pelo Codumi
		return nil
	case "post":
		if strings.HasPrefix(in.ToolName, "mcp__codumi__") {
			defer lockGuard(p)()
			g := loadGuard(p)
			g.At = time.Now().Unix()
			// os arquivos que a consulta apontou ficam liberados para ler
			for _, f := range rePathInText.FindAllString(string(in.ToolResponse), 400) {
				g.Files[strings.TrimPrefix(f, "./")] = true
			}
			g.save(p)
		}
		return nil
	case "pre":
		// consulta ao Codumi: anota já no pedido, porque o agente pode pedir
		// o Codumi e um comando na mesma rodada (o comando é checado antes de
		// a consulta terminar)
		if strings.HasPrefix(in.ToolName, "mcp__codumi__") {
			defer lockGuard(p)()
			g := loadGuard(p)
			g.At = time.Now().Unix()
			g.save(p)
			return nil
		}
		dir := in.Cwd
		if dir == "" {
			dir, _ = os.Getwd()
		}
		root, _, ok := store.Find(dir)
		if !ok || !isCodeRead(in.ToolName, in.ToolInput, root) {
			return nil // projeto sem mapa, ou não é leitura de código
		}
		defer lockGuard(p)()
		g := loadGuard(p)
		deny := func(reason string) error {
			g.save(p)
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"hookSpecificOutput": map[string]any{
				"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason,
			}})
		}
		// busca por um nome que está no mapa: a resposta do Codumi vem junto
		if pat := grepPattern(in.ToolName, in.ToolInput); pat != "" {
			if name := identOf(pat); name != "" && !g.Warned["grep:"+name] {
				if hint := refsHint(root, name); hint != "" {
					g.Warned["grep:"+name] = true
					g.At = time.Now().Unix()
					for _, f := range rePathInText.FindAllString(hint, 400) {
						g.Files[f] = true
					}
					return deny("Codumi already answers this search exactly (codumi refs " + name + "), so it was not run. Every use, with file and line:\n\n" + hint +
						"\n\nNext time call codumi_refs directly. If you still need the raw text search, run the same command again and it will pass.")
				}
			}
		}
		if g.At == 0 {
			// o Codumi pode não estar disponível nesta sessão (ainda conectando):
			// a mesma leitura tentada de novo passa, para não prender o agente
			key := "first:" + in.ToolName + ":" + firstTarget(in.ToolInput)
			if g.Warned[key] {
				return nil
			}
			g.Warned[key] = true
			return deny("This project has an exact Codumi index. Start every question by asking Codumi, before reading or searching files: " +
				"codumi_page for a screen (route like /app/ranking, or its component name), codumi_find for a text shown in the UI or in the code, " +
				"codumi_refs for who uses a function/component/type, codumi_route for an API route, codumi_table for a table or RPC, codumi_note for a module. " +
				"Codumi answers with file and line; then you can read the files it points to. (If the Codumi tools are not available in this session, run the same read again and it will pass.)")
		}
		// depois da consulta: ler um arquivo (Read, ou cat/sed/head/tail no
		// terminal) é liberado por arquivo
		var files []string
		switch in.ToolName {
		case "Read":
			fp, _ := in.ToolInput["file_path"].(string)
			files = []string{fp}
		case "Bash":
			c, _ := in.ToolInput["command"].(string)
			if reFileCat.MatchString(c) {
				files = rePathInText.FindAllString(c, 20)
			}
		}
		var fresh []string
		for _, fp := range files {
			rel := relIn(root, dir, fp)
			if rel == "" || nonCode[strings.ToLower(filepath.Ext(rel))] || nonCode[strings.ToLower(filepath.Base(rel))] {
				continue
			}
			if g.Files[rel] || g.Warned["read:"+rel] {
				g.Files[rel] = true
				continue
			}
			fresh = append(fresh, rel)
		}
		if len(fresh) == 0 {
			if len(files) > 0 {
				g.save(p)
			}
			return nil
		}
		for _, rel := range fresh {
			g.Warned["read:"+rel] = true
		}
		list := strings.Join(fresh, ", ")
		return deny("Codumi has not pointed to " + list + " in this question yet. Before reading, ask Codumi how it fits: codumi_note " + fresh[0] +
			" (what the module does and its rules), codumi_refs <name> (who uses something in it) or codumi_impact. " +
			"If you still need to read it after that, run the same read again and it will pass.")
	}
	return nil
}
