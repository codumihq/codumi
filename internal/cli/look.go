package cli

// codumi_look: o agente olha a página do projeto. Abre o endereço (por padrão
// o da prévia que o app do Codumi está rodando, gravado em .codumi/preview.json)
// num navegador Chrome/Edge/Brave/Chromium sem janela, tira um print e junta os
// erros do console. O print volta para o agente como imagem.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// imagens que a ferramenta devolve junto com o texto (PNG)
var outImages [][]byte

func PreviewFile(root string) string { return filepath.Join(root, ".codumi", "preview.json") }

func browserPath() string {
	var cands []string
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium", "Google Chrome Canary"} {
			cands = append(cands, "/Applications/"+app+".app/Contents/MacOS/"+app)
			if h, err := os.UserHomeDir(); err == nil {
				cands = append(cands, filepath.Join(h, "Applications", app+".app/Contents/MacOS", app))
			}
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if base != "" {
				cands = append(cands, filepath.Join(base, `Google\Chrome\Application\chrome.exe`), filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`), filepath.Join(base, `BraveSoftware\Brave-Browser\Application\brave.exe`))
			}
		}
	default:
		for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(n); err == nil {
				cands = append(cands, p)
			}
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

var reConsole = regexp.MustCompile(`CONSOLE[(:]\d+\)?\] "(.*)", source: (.*)$`)

// Look tira o print da página e devolve o texto (endereço e erros do console) e a imagem.
func Look(root, rawURL, path string, width int) (string, []byte, error) {
	if rawURL == "" {
		var pv struct {
			URL string `json:"url"`
		}
		if b, err := os.ReadFile(PreviewFile(root)); err == nil {
			json.Unmarshal(b, &pv)
		}
		if pv.URL == "" {
			return "", nil, errors.New("no preview is running. Ask the user to start it in Codumi (Panel › Preview › Run the project), or pass url")
		}
		rawURL = pv.URL
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", nil, errors.New("url must be http or https")
	}
	if path != "" {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		ref, err := url.Parse(path)
		if err != nil {
			return "", nil, errors.New("invalid path")
		}
		u = u.ResolveReference(ref)
	}
	br := browserPath()
	if br == "" {
		return "", nil, errors.New("no Chrome, Edge, Brave or Chromium found to open the page")
	}
	if width < 320 || width > 2560 {
		width = 1280
	}
	height := 800
	if width < 700 {
		height = 844
	}
	tmp, err := os.MkdirTemp("", "codumi-look-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(tmp)
	shot := filepath.Join(tmp, "page.png")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, br, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run", "--no-default-browser-check",
		"--user-data-dir="+filepath.Join(tmp, "profile"), fmt.Sprintf("--window-size=%d,%d", width, height),
		"--virtual-time-budget=6000", "--enable-logging=stderr", "--v=0", "--screenshot="+shot, u.String())
	outb, _ := cmd.CombinedOutput()
	img, err := os.ReadFile(shot)
	if err != nil || len(img) == 0 {
		return "", nil, fmt.Errorf("the page could not be captured: %s", lastLine(string(outb)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Screenshot of %s at %dx%d.\n", u.String(), width, height)
	var logs []string
	for _, l := range strings.Split(string(outb), "\n") {
		if m := reConsole.FindStringSubmatch(l); m != nil {
			logs = append(logs, "- "+m[1]+" ("+m[2]+")")
		}
	}
	if len(logs) > 20 {
		logs = logs[len(logs)-20:]
	}
	if len(logs) > 0 {
		b.WriteString("Console messages:\n" + strings.Join(logs, "\n") + "\n")
	} else {
		b.WriteString("No console messages.\n")
	}
	return b.String(), img, nil
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	return ls[len(ls)-1]
}
