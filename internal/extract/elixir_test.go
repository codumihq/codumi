package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestElixir(t *testing.T) {
	g := &model.Graph{}
	if err := Elixir("testdata/elixir", g, nil); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	for _, p := range g.Pages {
		got += "PAGE " + p.Path + " " + p.Component + "\n"
	}
	expectLines(t, got, []string{
		"DECL module Loja.Vendas lib/loja/vendas.ex:1",
		"DECL function Loja.Vendas.listar lib/loja/vendas.ex:6",
		"DECL function Loja.Vendas.buscar lib/loja/vendas.ex:18",
		"DECL function Loja.Vendas.Pedido.changeset",
		"REF lib/loja/vendas.ex:12 [Loja.Vendas.criar] call -> Loja.Vendas.Pedido.changeset",
		"REF lib/loja/vendas.ex:11 [Loja.Vendas.criar] new -> Loja.Vendas.Pedido",
		"REF lib/loja/vendas.ex:16 [Loja.Vendas.total] call -> Loja.Vendas.buscar",
		"REF lib/loja_web/controllers/pedido_controller.ex:6 [LojaWeb.PedidoController.index] call -> Loja.Vendas.listar",
		"DB table pedidos [select] from=Loja.Vendas.listar",
		"DB table pedidos [insert] from=Loja.Vendas.criar",
		"DB table pedidos [select] from=Loja.Vendas.buscar",
		"CALL  /api/estoque from=LojaWeb.PedidoController.create",
		"ROUTE GET /api/pedidos handler=LojaWeb.PedidoController.index",
		"ROUTE POST /api/pedidos handler=LojaWeb.PedidoController.create",
		"ROUTE GET /api/pedidos/:pedido_id/recibo handler=LojaWeb.PedidoController.recibo",
		"ROUTE GET /api/saude handler=LojaWeb.SaudeController.show",
		"ROUTE GET /painel handler=LojaWeb.PainelLive.mount",
		"PAGE /painel LojaWeb.PainelLive",
	})
}

func TestPhoenixGuards(t *testing.T) {
	g := &model.Graph{}
	if err := Elixir("testdata/elixir", g, nil); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range g.Routes {
		got[r.Method+" "+r.Path] = r.Guards
	}
	if got["POST /api/pedidos"] != "api,accepts,auth,require_authenticated_user" || got["GET /api/saude"] != "api,accepts" {
		t.Errorf("guardas: %v", got)
	}
}
