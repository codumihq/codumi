package extract

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

// TanStack Router/Start, React Router (JSX e objetos) e SvelteKit +server.ts.
func TestTSFrameworks(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not in PATH")
	}
	tsPath, _ := filepath.Abs("../../examples/demo/node_modules/typescript")
	if _, err := os.Stat(tsPath); err != nil {
		t.Skip("examples/demo/node_modules missing (npm i there)")
	}
	t.Setenv("CODUMI_TYPESCRIPT", tsPath)
	root, _ := filepath.Abs("testdata/tsfw")
	g := &model.Graph{Root: root}
	if err := TypeScript(root, g, nil); err != nil {
		t.Fatal(err)
	}
	if err := SFC(root, g); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	for _, p := range g.Pages {
		got += "PAGE " + p.Path + " " + p.Component + "\n"
	}
	expectLines(t, got, []string{
		"PAGE /posts/$id PostPage",
		"PAGE / Layout",
		"PAGE / Home",
		"PAGE /pedidos Orders",
		"PAGE /pedidos/:id Order",
		"PAGE /conta Layout",
		"PAGE /conta/config Settings",
		"ROUTE GET /api/users handler=ServerRoute",
		"ROUTE POST /api/users handler=ServerRoute",
		"ROUTE GET /api/pedidos handler=GET",
		// Svelte e Astro
		"PAGE /pedidos/[id] +page",
		"PAGE /blog index",
		"REF src/routes/pedidos/[id]/+page.svelte:2 [<module>] import -> Botao",
		"REF src/routes/pedidos/[id]/+page.svelte:6 [+page] call -> buscar",
		"REF src/routes/pedidos/[id]/+page.svelte:13 [+page] call -> formatar",
		"REF src/routes/pedidos/[id]/+page.svelte:14 [+page] jsx -> Botao",
		"REF src/pages/blog/index.astro:4 [index] call -> formatar",
		"REF src/pages/blog/index.astro:6 [index] jsx -> Botao",
		"DB table orders [update] from=+page",
		"CALL POST /api/pedidos/${}/pago from=+page",
	})
}
