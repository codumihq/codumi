package extract

import (
	"fmt"
	"strings"
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

// dumpGraph escreve o grafo em linhas fáceis de procurar nos testes.
func dumpGraph(g *model.Graph) string {
	var b strings.Builder
	for _, d := range g.Decls {
		fmt.Fprintf(&b, "DECL %s %s %s:%d exp=%v\n", d.Kind, d.Name, d.File, d.Line, d.Exported)
	}
	for _, r := range g.Refs {
		fmt.Fprintf(&b, "REF %s:%d [%s] %s -> %s\n", r.File, r.Line, r.From, r.Kind, r.To)
	}
	for _, r := range g.Routes {
		fmt.Fprintf(&b, "ROUTE %s %s handler=%s\n", r.Method, r.Path, r.Handler)
	}
	for _, r := range g.RouteCalls {
		fmt.Fprintf(&b, "CALL %s %s from=%s\n", r.Method, r.Path, r.From)
	}
	for _, u := range g.DBUses {
		fmt.Fprintf(&b, "DB %s %s %v from=%s\n", u.Type, u.Name, u.Ops, u.From)
	}
	return b.String()
}

func expectLines(t *testing.T, got string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("faltou: %s", w)
		}
	}
	if t.Failed() {
		t.Log("\n" + got)
	}
}

func TestKotlinJava(t *testing.T) {
	g := &model.Graph{}
	if err := Kotlin("testdata/jvm", g, nil); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	expectLines(t, got, []string{
		"DECL class PedidoRepo src/main/kotlin/com/loja/pedidos/Pedidos.kt:8 exp=true",
		"DECL method PedidoRepo.registrar src/main/kotlin/com/loja/pedidos/Pedidos.kt:14 exp=false",
		"DECL method PedidoRepo.padrao",
		"DECL function semEspacos",
		"DECL method PedidoController.formatar src/main/java/com/loja/web/PedidoController.java:22 exp=false",
		"REF src/main/kotlin/com/loja/api/Rotas.kt:13 [rotas] call -> PedidoRepo.padrao",
		"REF src/main/kotlin/com/loja/api/Rotas.kt:29 [montar] call -> Pedido",  // import com alias (P)
		"REF src/main/kotlin/com/loja/api/Rotas.kt:31 [montar] call -> cliente", // import com *
		"REF src/main/kotlin/com/loja/pedidos/Pedidos.kt:11 [PedidoRepo.marcarPago] call -> PedidoRepo.registrar",
		"REF src/main/java/com/loja/web/PedidoController.java:19 [PedidoController.cancelar] call -> Auditoria.registrar",
		"REF src/main/java/com/loja/web/PedidoController.java:3 [<module>] import -> PedidoRepo", // Java vendo Kotlin
		"ROUTE GET /v1/pedidos/{id} handler=PedidoController.buscar",
		"ROUTE POST /v1/pedidos/{id}/cancelar handler=PedidoController.cancelar",
		"ROUTE POST /api/pedidos/{id}/pago handler=rotas",
		"CALL POST /api/pedidos/{id}/pago from=LojaApi.pagar",
		"DB table orders [update] from=PedidoRepo.marcarPago",
		"DB table orders [select] from=PedidoController.buscar",
	})
	if strings.Contains(got, "CALL  /api from") {
		t.Error("route(\"/api\") do Ktor não é chamada de API")
	}
}
