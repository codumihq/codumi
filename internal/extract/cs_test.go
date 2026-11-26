package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestCSharp(t *testing.T) {
	g := &model.Graph{}
	if err := Kotlin("testdata/cs", g, nil); err != nil {
		t.Fatal(err)
	}
	expectLines(t, dumpGraph(g), []string{
		"DECL class PedidosController Api/Controllers/PedidosController.cs:9 exp=true",
		"DECL method PedidosController.Buscar Api/Controllers/PedidosController.cs:15 exp=true",
		"DECL method PedidosController.Formatar Api/Controllers/PedidosController.cs:29 exp=false",
		"DECL method Cobranca.Registrar",
		"DECL class Cliente", // record
		"REF Api/Controllers/PedidosController.cs:18 [PedidosController.Buscar] call -> PedidosController.Formatar",
		"REF Api/Controllers/PedidosController.cs:24 [PedidosController.Pagar] call -> Cobranca.Registrar",
		"REF Api/Controllers/PedidosController.cs:25 [PedidosController.Pagar] new -> Pedido",
		"REF Api/Controllers/PedidosController.cs:29 [PedidosController.Formatar] ref -> Pedido", // corpo => …
		"ROUTE GET /api/pedidos/{id} handler=PedidosController.Buscar",
		"ROUTE POST /api/pedidos/{id}/pagar handler=PedidosController.Pagar",
		"ROUTE GET /health handler=<module>",
		"ROUTE POST /api/cobranca/{id} handler=Cobranca.Registrar",
		"DB table orders [select] from=PedidosController.Buscar",
		"DB table orders [update] from=PedidosController.Pagar",
		"DB table payment_events [insert] from=Cobranca.Registrar",
		"CALL  /api/saude from=Cobranca.Saude",
	})
}

func TestCSharpGuards(t *testing.T) {
	g := &model.Graph{}
	if err := Kotlin("testdata/cs", g, nil); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range g.Routes {
		got[r.Method+" "+r.Path] = r.Guards
	}
	if got["POST /api/pedidos/{id}/pagar"] != "Authorize" || got["GET /api/pedidos/{id}"] != "none" {
		t.Errorf("guardas: %v", got)
	}
}
