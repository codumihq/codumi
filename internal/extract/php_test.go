package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestPHPLaravel(t *testing.T) {
	g := &model.Graph{}
	if err := PHP("testdata/php", g, nil); err != nil {
		t.Fatal(err)
	}
	expectLines(t, dumpGraph(g), []string{
		"DECL class PedidoController app/Http/Controllers/PedidoController.php:10 exp=true",
		"DECL method PedidoController.show app/Http/Controllers/PedidoController.php:13 exp=true",
		"DECL method Cobranca.avisar app/Services/Cobranca.php:17 exp=false",
		"DECL function formatar app/Services/Cobranca.php:24 exp=true",
		"REF app/Http/Controllers/PedidoController.php:13 [PedidoController.show] type -> Pedido",
		"REF app/Http/Controllers/PedidoController.php:16 [PedidoController.show] call -> formatar",
		"REF app/Http/Controllers/PedidoController.php:22 [PedidoController.pagar] new -> Cobranca",
		"REF app/Services/Cobranca.php:14 [Cobranca.pagar] call -> Cobranca.avisar",
		"ROUTE GET /api/pedidos/{pedido} handler=PedidoController.show",
		"ROUTE POST /api/admin/pedidos/{id}/pagar handler=PedidoController.pagar",
		"ROUTE GET /api/clientes handler=PedidoController.index",
		"ROUTE DELETE /api/clientes/{cliente} handler=PedidoController.destroy",
		"ROUTE GET /api/saude handler=<module>",
		"DB table pedidos [update] from=Cobranca.pagar",
		"DB table payment_events [insert] from=Cobranca.pagar",
		"DB table order_items [select] from=PedidoController.show",
		"DB table orders [select] from=Cobranca.avisar",
		"CALL  /api/avisos from=Cobranca.avisar",
	})
}

func TestLaravelGuards(t *testing.T) {
	g := &model.Graph{}
	if err := PHP("testdata/php", g, nil); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range g.Routes {
		got[r.Method+" "+r.Path] = r.Guards
	}
	if got["POST /api/admin/pedidos/{id}/pagar"] != "auth:sanctum" || got["GET /api/pedidos/{pedido}"] != "none" || got["GET /api/saude"] != "none" {
		t.Errorf("guardas: %v", got)
	}
}
