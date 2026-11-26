package extract

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

//go:embed extract.py
var pyScript []byte

func python() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python3 was not found in PATH (needed to read Python)")
}

// Python extracts the Python code under root with the stdlib ast module.
// If only is non-nil, just those files (relative to root) are walked; the
// whole tree is still parsed so imports resolve.
func Python(root string, g *model.Graph, only []string) error {
	py, err := python()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "codumi-py-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "codumi_extract.py")
	if err := os.WriteFile(script, pyScript, 0o600); err != nil {
		return err
	}
	args := []string{"-I", script, root}
	if only != nil {
		lst := filepath.Join(dir, "only.txt")
		if err := os.WriteFile(lst, []byte(strings.Join(only, "\n")+"\n"), 0o600); err != nil {
			return err
		}
		args = append(args, lst)
	}
	cmd := exec.Command(py, args...)
	cmd.Dir = dir // never import anything from the project
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done, scanErr := decode(stdout, g)
	if scanErr != nil {
		return scanErr
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("Python extractor failed: %w", err)
	}
	if !done {
		return fmt.Errorf("Python extractor stopped before finishing")
	}
	return nil
}
