package extract

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codumihq/codumi/internal/model"
)

//go:embed dart/pubspec.yaml dart/bin/extract.dart
var dartFiles embed.FS

// DartProjects returns the directories under root holding a pubspec.yaml.
func DartProjects(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if p != root && (strings.HasPrefix(n, ".") || n == "build" || n == "node_modules" || n == "ios" || n == "android" || n == "macos" || n == "windows" || n == "linux" || n == "web") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "pubspec.yaml" {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	return out
}

// dartTool builds (once) a native executable of the Dart extractor in the
// user cache dir, keyed by a hash of its sources.
func dartTool() (string, error) {
	if _, err := exec.LookPath("dart"); err != nil {
		return "", fmt.Errorf("dart was not found in PATH (needed to read Dart)")
	}
	h := sha256.New()
	fs.WalkDir(dartFiles, "dart", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := dartFiles.ReadFile(p)
			h.Write([]byte(p))
			h.Write(b)
		}
		return nil
	})
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	dir := filepath.Join(cache, "codumi", "dart-"+hex.EncodeToString(h.Sum(nil))[:12])
	exe := filepath.Join(dir, "codumi-dart")
	if _, err := os.Stat(exe); err == nil {
		return exe, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		return "", err
	}
	for _, f := range []string{"pubspec.yaml", "bin/extract.dart"} {
		b, _ := dartFiles.ReadFile("dart/" + f)
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			return "", err
		}
	}
	fmt.Fprintln(os.Stderr, "codumi: preparing the Dart reader (first run only, about 30s)…")
	run := func(args ...string) error {
		cmd := exec.Command("dart", args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("dart %s: %v\n%s", strings.Join(args, " "), err, b)
		}
		return nil
	}
	if err := run("pub", "get"); err != nil {
		return "", err
	}
	tmp := exe + ".tmp"
	if err := run("compile", "exe", "bin/extract.dart", "-o", tmp); err != nil {
		return "", err
	}
	return exe, os.Rename(tmp, exe)
}

// Dart extracts every Dart/Flutter package under root with package:analyzer.
// If only is non-nil, just those files (relative to root) are walked.
func Dart(root string, g *model.Graph, only []string) error {
	exe, err := dartTool()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "codumi-dart-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	args := []string{root}
	if only != nil {
		lst := filepath.Join(dir, "only.txt")
		if err := os.WriteFile(lst, []byte(strings.Join(only, "\n")+"\n"), 0o600); err != nil {
			return err
		}
		args = append(args, lst)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = root
	cache, _ := os.UserCacheDir()
	os.MkdirAll(filepath.Join(cache, "codumi", "dart-summaries"), 0o755)
	cmd.Env = append(os.Environ(), "CODUMI_DART_SDK="+dartSDK(), "CODUMI_DART_CACHE="+filepath.Join(cache, "codumi", "dart-summaries"))
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
		return fmt.Errorf("Dart extractor failed: %w", err)
	}
	if !done {
		return fmt.Errorf("Dart extractor stopped before finishing")
	}
	return nil
}

// dartSDK finds the SDK behind the `dart` in PATH: a plain SDK
// (…/dart-sdk/bin/dart) or Flutter's wrapper (…/flutter/bin/dart, with the
// SDK in bin/cache/dart-sdk).
func dartSDK() string {
	if v := os.Getenv("CODUMI_DART_SDK"); v != "" {
		return v
	}
	p, err := exec.LookPath("dart")
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	bin := filepath.Dir(p)
	for _, c := range []string{filepath.Dir(bin), filepath.Join(bin, "cache", "dart-sdk")} {
		if _, err := os.Stat(filepath.Join(c, "version")); err == nil {
			if _, err := os.Stat(filepath.Join(c, "lib")); err == nil {
				return c
			}
		}
	}
	return ""
}
