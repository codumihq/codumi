package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/codumihq/codumi/internal/store"
)

const (
	hookBegin = "# >>> codumi >>>"
	hookEnd   = "# <<< codumi <<<"
)

var hookNames = []string{"post-commit", "post-checkout", "post-merge", "post-rewrite"}

func gitOut(args ...string) (string, error) {
	b, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(b)), err
}

func cmdHook(pos []string, flags map[string]string) error {
	if len(pos) == 0 || (pos[0] != "install" && pos[0] != "uninstall") {
		return errors.New("usage: codumi hook install | codumi hook uninstall")
	}
	top, err := gitOut("rev-parse", "--show-toplevel")
	if err != nil {
		return errors.New("not inside a git repository")
	}
	dir, err := gitOut("rev-parse", "--git-path", "hooks")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(dir) {
		cwd, _ := os.Getwd()
		dir = filepath.Join(cwd, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	bin, _ := os.Executable()
	block := fmt.Sprintf(`%s
# Re-index in the background so agents always query a fresh map.
R=%q; [ -x "$R" ] || R=codumi
command -v "$R" >/dev/null 2>&1 && ( cd "$(git rev-parse --show-toplevel)" && nohup "$R" index --quiet >/dev/null 2>&1 & )
%s
`, hookBegin, bin, hookEnd)

	for _, name := range hookNames {
		p := filepath.Join(dir, name)
		cur, _ := os.ReadFile(p)
		clean := stripBlock(string(cur))
		if pos[0] == "uninstall" {
			if strings.TrimSpace(clean) == "#!/bin/sh" || strings.TrimSpace(clean) == "" {
				os.Remove(p)
			} else if clean != string(cur) {
				os.WriteFile(p, []byte(clean), 0o755)
			}
			continue
		}
		if clean == "" {
			clean = "#!/bin/sh\n"
		}
		if !strings.HasSuffix(clean, "\n") {
			clean += "\n"
		}
		if err := os.WriteFile(p, []byte(clean+block), 0o755); err != nil {
			return err
		}
	}
	if pos[0] == "uninstall" {
		fmt.Fprintln(out, "removed codumi from the git hooks")
		return nil
	}
	fmt.Fprintf(out, "installed: %s re-index %s in the background\n", strings.Join(hookNames, ", "), top)
	fmt.Fprintln(out, "existing hooks were kept; `codumi hook uninstall` removes only the codumi block")
	return nil
}

// stripBlock tira o bloco do Codumi (e o do Rastro, nome antigo) de um hook.
func stripBlock(s string) string {
	return stripMarked(stripMarked(s, hookBegin, hookEnd), "# >>> rastro >>>", "# <<< rastro <<<")
}

func stripMarked(s, hookBegin, hookEnd string) string {
	for {
		i := strings.Index(s, hookBegin)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], hookEnd)
		if j < 0 {
			return s[:i]
		}
		end := i + j + len(hookEnd)
		if end < len(s) && s[end] == '\n' {
			end++
		}
		s = s[:i] + s[end:]
	}
}

// lockIndex makes sure only one index runs at a time per project. A lock
// older than 10 minutes is considered stale.
func lockIndex(root string) (func(), error) {
	store.Migrate(root)
	dir := filepath.Join(root, store.Dir)
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "index.lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(p) }, nil
		}
		if st, err2 := os.Stat(p); err2 == nil && time.Since(st.ModTime()) > 10*time.Minute {
			os.Remove(p)
			continue
		}
		break
	}
	return nil, errors.New("another `codumi index` is running for this project")
}
