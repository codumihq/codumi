package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestScala(t *testing.T) {
	g := &model.Graph{}
	if err := Kotlin("testdata/scala", g, nil); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	expectLines(t, got, []string{
		"DECL interface Repo app/services/Pedidos.scala:5",
		"DECL class PedidoService app/services/Pedidos.scala:9",
		"DECL method PedidoService.listar app/services/Pedidos.scala:10",
		"DECL method PedidoService.validar",
		"DECL method Pedidos.index app/controllers/Pedidos.scala:7",
		"REF app/services/Pedidos.scala:11 [PedidoService.listar] call -> PedidoService.validar",
		"REF app/controllers/Pedidos.scala:3 [<module>] import -> PedidoService",
		"REF app/controllers/Pedidos.scala:8 [Pedidos.index] call -> PedidoService.listar",
		"ROUTE GET /pedidos handler=Pedidos.index",
		"ROUTE POST /pedidos/:id handler=Pedidos.salvar",
	})
}
