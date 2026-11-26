// Package extract runs the embedded TypeScript extractor with Node and
// decodes its output.
package extract

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

//go:embed extract.js
var script []byte

// Item is one line of extractor output. Only the fields of its kind are set.
type Item struct {
	K        string   `json:"k"`
	F        string   `json:"f"`
	Lines    int      `json:"lines"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Line     int      `json:"line"`
	Exported bool     `json:"exported"`
	From     string   `json:"from"`
	TF       string   `json:"tf"`
	To       string   `json:"to"`
	Type     string   `json:"type"`
	Ops      []string `json:"ops"`
	Client   string   `json:"client"`
	Method   string   `json:"method"`
	Path     string   `json:"path"`
	Files    int      `json:"files"`
	Handler  string   `json:"handler"`
	Q        string   `json:"q"`
	Guards   string   `json:"guards"`
}

// TypeScript extracts the code graph of the project at root.
// If only is non-nil, just those files (relative to root) are walked.
func TypeScript(root string, g *model.Graph, only []string) error {
	if _, err := exec.LookPath("node"); err != nil {
		return fmt.Errorf("node was not found in PATH (needed to read TypeScript)")
	}
	dir, err := os.MkdirTemp("", "codumi-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	js := filepath.Join(dir, "extract.cjs")
	if err := os.WriteFile(js, script, 0o600); err != nil {
		return err
	}

	args := []string{"--max-old-space-size=8192", js, root}
	if only != nil {
		lst := filepath.Join(dir, "only.txt")
		if err := os.WriteFile(lst, []byte(strings.Join(only, "\n")+"\n"), 0o600); err != nil {
			return err
		}
		args = append(args, lst)
	}
	cmd := exec.Command("node", args...)
	cmd.Dir = root
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
		return fmt.Errorf("TypeScript extractor failed: %w", err)
	}
	if !done {
		return fmt.Errorf("TypeScript extractor stopped before finishing")
	}
	return nil
}

// decode reads extractor NDJSON (shared by the TypeScript and Dart
// extractors) into g. It reports whether the "done" line arrived.
func decode(r io.Reader, g *model.Graph) (bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	done := false
	for sc.Scan() {
		var it Item
		if err := json.Unmarshal(sc.Bytes(), &it); err != nil {
			continue
		}
		switch it.K {
		case "file":
			g.Files = append(g.Files, model.File{Path: it.F, Lines: it.Lines})
		case "decl":
			g.Decls = append(g.Decls, model.Decl{File: it.F, Name: it.Name, Kind: it.Kind, Line: it.Line, Exported: it.Exported})
		case "ref":
			g.Refs = append(g.Refs, model.Ref{File: it.F, Line: it.Line, From: it.From, ToFile: it.TF, To: it.To, Kind: it.Kind})
		case "db":
			g.DBUses = append(g.DBUses, model.DBUse{File: it.F, Line: it.Line, From: it.From, Type: it.Type, Name: it.Name, Ops: it.Ops, Client: it.Client})
		case "page":
			g.Pages = append(g.Pages, model.Page{File: it.F, Path: it.Path, Line: it.Line, Component: it.Name})
		case "route":
			h := it.Handler
			if h == "" {
				h = it.Method
			}
			g.Routes = append(g.Routes, model.Route{File: it.F, Method: it.Method, Path: it.Path, Line: it.Line, HandlerFile: it.F, Handler: h, Guards: it.Guards})
		case "sqlq":
			for _, t := range sqlTables(it.Q) {
				g.DBUses = append(g.DBUses, model.DBUse{File: it.F, Line: it.Line, From: it.From, Type: "table", Name: t.name, Ops: t.ops, Client: it.Client})
			}
		case "call_route":
			g.RouteCalls = append(g.RouteCalls, model.RouteCall{File: it.F, Line: it.Line, From: it.From, Path: it.Path, Method: it.Method})
		case "done":
			done = true
		}
	}
	return done, sc.Err()
}
