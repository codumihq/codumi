package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestRails(t *testing.T) {
	g := &model.Graph{}
	if err := Ruby("testdata/rails", g, nil); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	expectLines(t, got, []string{
		"DECL class Pedido app/models/pedido.rb:1",
		"DECL method Pedido.recentes app/models/pedido.rb:6",
		"DECL method Pedido.normalizar app/models/pedido.rb:16",
		"DECL class Cadastro::Pessoa app/models/pessoa.rb:2",
		"DECL method Cobranca::Gerar.call app/services/cobranca.rb:7",
		"REF app/models/pedido.rb:3 [Pedido] ref -> Pedido.normalizar",
		"REF app/services/cobranca.rb:10 [Cobranca::Gerar.call] call -> Cobranca::Gerar.registrar",
		"REF app/controllers/pedidos_controller.rb:6 [PedidosController.index] call -> Pedido.recentes",
		"REF app/controllers/pedidos_controller.rb:14 [PedidosController.create] new -> Cobranca::Gerar",
		"REF app/controllers/pedidos_controller.rb:2 [PedidosController] ref -> PedidosController.carregar",
		"DB table pedidos [update] from=Cobranca::Gerar.call",
		"REF app/services/cobranca.rb:20 [Cobranca::Gerar.avulso] call -> Cobranca::Gerar.registrar",
		"DB table clientes [select] from=Cobranca::Gerar.call",
		"DB table pedidos [insert] from=PedidosController.create",
		"DB table pedidos [select] from=PedidosController.carregar",
		"DB table caixas [update] from=Cobranca::Gerar.registrar",
		"CALL  /api/estoque from=Cobranca::Gerar.call",
		"ROUTE GET / handler=PedidosController.index",
		"ROUTE GET /pedidos handler=PedidosController.index",
		"ROUTE GET /pedidos/:id handler=PedidosController.show",
		"ROUTE POST /pedidos handler=PedidosController.create",
		"ROUTE POST /pedidos/:id/pagar handler=PedidosController.pagar",
		"ROUTE GET /pedidos/:pedido_id/itens handler=ItensController.index",
		"ROUTE GET /admin/vendas handler=Admin::RelatoriosController.vendas",
		"ROUTE GET /api/v1/saude handler=SaudeController.ver",
		"ROUTE GET /debug handler=DebugController.ver",
		"ROUTE DELETE /sair handler=SessoesController.destroy",
	})
}

func TestRailsGuards(t *testing.T) {
	g := &model.Graph{}
	if err := Ruby("testdata/rails", g, nil); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range g.Routes {
		got[r.Method+" "+r.Path] = r.Guards
	}
	if got["GET /pedidos"] != "none" || got["GET /pedidos/:id"] != "authenticate_user!,carregar" || got["POST /pedidos"] != "authenticate_user!" {
		t.Errorf("guardas: %v", got)
	}
}
