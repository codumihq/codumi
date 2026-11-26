package cli

import (
	"bytes"
	"os"
	"sync"
)

var runMu sync.Mutex

// Run executes a codumi command in dir and returns what it printed (stdout,
// then side notes) and its exit code. Commands share process-wide state
// (working directory, output writers), so calls are serialized.
func Run(dir string, args ...string) (output, sideNotes string, code int) {
	runMu.Lock()
	defer runMu.Unlock()
	prevDir, _ := os.Getwd()
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			return "", err.Error(), 1
		}
		defer os.Chdir(prevDir)
	}
	var o, n bytes.Buffer
	prevOut, prevNotes := out, notes
	out, notes = &o, &n
	defer func() { out, notes = prevOut, prevNotes }()

	// Errors are printed to stderr by Main; capture them as notes.
	// Read it while the command runs, so a chatty extractor can't fill the
	// pipe and block.
	r, w, err := os.Pipe()
	prevErr := os.Stderr
	var e bytes.Buffer
	done := make(chan struct{})
	if err == nil {
		os.Stderr = w
		go func() { e.ReadFrom(r); close(done) }()
	}
	code = Main(args)
	if err == nil {
		os.Stderr = prevErr
		w.Close()
		<-done
		r.Close()
		n.Write(e.Bytes())
	}
	return o.String(), n.String(), code
}

// Serve runs the MCP server on stdin/stdout for the project in dir (the
// desktop app does this when launched as `Codumi mcp`).
func Serve(dir string) int {
	args := []string{"mcp"}
	if dir != "" {
		args = append(args, dir)
	}
	return Main(args)
}
