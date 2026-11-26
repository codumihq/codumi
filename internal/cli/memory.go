package cli

// Memória do projeto: fatos que valem entre conversas (decisões, convenções,
// pegadinhas, preferências que o usuário disse). Fica em .codumi/memory.md,
// fora do git (a .codumi só versiona as notas), uma linha por fato:
//
//	- Use pnpm, nunca npm (2026-11-13)
//
// O agente grava com codumi_remember; o app mostra, edita e manda a memória
// no começo de cada conversa.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/codumihq/codumi/internal/store"
)

type MemItem struct {
	Text string `json:"text"`
	Date string `json:"date"` // AAAA-MM-DD
}

const memMax = 120 // fatos; o mais antigo sai quando passa

var reMemLine = regexp.MustCompile(`^\s*[-*]\s+(.+?)(?:\s+\((\d{4}-\d{2}-\d{2})\))?\s*$`)

func memPath(root string) string {
	// compartilhada com o time: fica em .codumi/team, que vai para o git
	if t := teamMemPath(root); fileExistsCli(t) {
		return t
	}
	return filepath.Join(root, store.Dir, "memory.md")
}

func teamMemPath(root string) string { return filepath.Join(root, store.Dir, "team", "memory.md") }

func fileExistsCli(p string) bool { _, err := os.Stat(p); return err == nil }

// MemoryShared diz se a memória do projeto vai para o git (compartilhada com o time).
func MemoryShared(root string) bool { return fileExistsCli(teamMemPath(root)) }

// MemoryShare move a memória para .codumi/team (vai para o git) ou de volta.
func MemoryShare(root string, on bool) error {
	local := filepath.Join(root, store.Dir, "memory.md")
	team := teamMemPath(root)
	os.WriteFile(filepath.Join(root, store.Dir, ".gitignore"), []byte(store.GitIgnore), 0o644)
	if on {
		if fileExistsCli(team) {
			return nil
		}
		os.MkdirAll(filepath.Dir(team), 0o755)
		if fileExistsCli(local) {
			return os.Rename(local, team)
		}
		return os.WriteFile(team, []byte(""), 0o644)
	}
	if !fileExistsCli(team) {
		return nil
	}
	return os.Rename(team, local)
}

// MemoryRead lê a memória do projeto em root.
func MemoryRead(root string) []MemItem {
	b, err := os.ReadFile(memPath(root))
	if err != nil {
		return []MemItem{}
	}
	out := []MemItem{}
	for _, l := range strings.Split(string(b), "\n") {
		if m := reMemLine.FindStringSubmatch(l); m != nil {
			out = append(out, MemItem{Text: strings.TrimSpace(m[1]), Date: m[2]})
		}
	}
	return out
}

// MemoryWrite grava a memória inteira.
func MemoryWrite(root string, items []MemItem) error {
	var b strings.Builder
	b.WriteString("# Memória do projeto\n\nFatos guardados pelo Codumi entre conversas. Um por linha; pode editar.\n\n")
	n := 0
	start := 0
	if len(items) > memMax {
		start = len(items) - memMax
	}
	for _, it := range items[start:] {
		t := strings.Join(strings.Fields(it.Text), " ")
		if t == "" {
			continue
		}
		if it.Date != "" {
			fmt.Fprintf(&b, "- %s (%s)\n", t, it.Date)
		} else {
			fmt.Fprintf(&b, "- %s\n", t)
		}
		n++
	}
	if err := os.MkdirAll(filepath.Join(root, store.Dir), 0o755); err != nil {
		return err
	}
	p := memPath(root)
	if err := os.WriteFile(p+".tmp", []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func memNorm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// Remember acrescenta um fato (ou troca um parecido) e tira os que forget
// indicar. Devolve o que fez, para o agente.
func Remember(root, fact, forget string) (string, error) {
	items := MemoryRead(root)
	var msg []string
	if f := memNorm(forget); f != "" {
		keep := items[:0]
		for _, it := range items {
			if strings.Contains(memNorm(it.Text), f) {
				msg = append(msg, "forgot: "+it.Text)
				continue
			}
			keep = append(keep, it)
		}
		items = keep
	}
	if fact = strings.Join(strings.Fields(fact), " "); fact != "" {
		if len([]rune(fact)) > 400 {
			return "", fmt.Errorf("keep it to one short sentence (got %d characters)", len([]rune(fact)))
		}
		dup := false
		for _, it := range items {
			if memNorm(it.Text) == memNorm(fact) {
				dup = true
			}
		}
		if dup {
			msg = append(msg, "already remembered: "+fact)
		} else {
			items = append(items, MemItem{Text: fact, Date: time.Now().Format("2006-01-02")})
			msg = append(msg, "remembered: "+fact)
		}
	}
	if len(msg) == 0 {
		return "", fmt.Errorf("pass fact (to save) or forget (text of the fact to remove)")
	}
	if err := MemoryWrite(root, items); err != nil {
		return "", err
	}
	return strings.Join(msg, "\n"), nil
}

// MemoryPrompt é o trecho que vai para o agente no começo da conversa.
func MemoryPrompt(root string) string {
	items := MemoryRead(root)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Project memory (facts saved in earlier conversations with this project; follow them, and fix one with codumi_remember if it is wrong or outdated):\n")
	for _, it := range items {
		b.WriteString("- " + it.Text + "\n")
	}
	return b.String()
}
