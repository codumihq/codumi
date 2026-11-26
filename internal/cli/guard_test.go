package cli

import "testing"

func TestIsCodeRead(t *testing.T) {
	root := "/proj"
	yes := []string{
		`cd "/proj" && grep -rln "custom_fields" src/pages | head`,
		`cd "/proj" && grep -n "ranking" src/components/layout/MainLayout.tsx | head`,
		`wc -l src/pages/Ranking.tsx; grep -n "Download" src/pages/Ranking.tsx`,
		`sed -n 1640,1656p src/pages/Ranking.tsx`,
		`rg useCustomFields src`,
		`find src -name "*.tsx"`,
		`cat src/App.tsx`,
	}
	no := []string{
		`git log --oneline | grep fix`,
		`npm run build 2>&1 | tail -20`,
		`git status && git diff --stat`,
		`vercel ls | grep Ready`,
		`echo "use grep -rn here"`,
		`npx tsc --noEmit | head`,
	}
	for _, c := range yes {
		if !isCodeRead("Bash", map[string]any{"command": c}, root) {
			t.Errorf("devia barrar: %s", c)
		}
	}
	for _, c := range no {
		if isCodeRead("Bash", map[string]any{"command": c}, root) {
			t.Errorf("não devia barrar: %s", c)
		}
	}
	if !isCodeRead("Read", map[string]any{"file_path": "/proj/src/App.tsx"}, root) || isCodeRead("Read", map[string]any{"file_path": "/Users/x/Downloads/print.png"}, root) {
		t.Error("Read do projeto sim, de fora não")
	}
	if !isCodeRead("Grep", nil, root) {
		t.Error("Grep sim")
	}
}
