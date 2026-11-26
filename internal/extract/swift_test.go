package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestSwift(t *testing.T) {
	g := &model.Graph{}
	if err := Swift("testdata/swift", g, nil); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	expectLines(t, got, []string{
		"DECL struct Pedido App/Pedidos.swift:3",
		"DECL method Pedido.total App/Pedidos.swift:5",
		"DECL class PedidoService App/Pedidos.swift:8",
		"DECL method PedidoService.listarCache App/Pedidos.swift:22",
		"DECL component PedidosView App/Telas.swift:3",
		"DECL function log App/Pedidos.swift:25",
		"REF App/Pedidos.swift:9 [PedidoService.listar] type -> Pedido",
		"REF App/Pedidos.swift:16 [PedidoService.salvar] call -> PedidoService.listarCache",
		"REF App/Pedidos.swift:17 [PedidoService.salvar] call -> log",
		"REF App/Telas.swift:4 [PedidosView] new -> PedidoService",
		"REF App/Telas.swift:6 [PedidosView] new -> Linha",
		"REF App/Telas.swift:7 [PedidosView] call -> PedidoService.listar",
		"CALL  /api/pedidos from=PedidoService.listar",
		"DB table pedidos [select] from=PedidoService.listar",
		"ROUTE GET /api/v1/pedidos/:id handler=mostrar",
		"ROUTE POST /saida handler=routes",
	})
}
