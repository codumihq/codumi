package extract

import (
	"strings"
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestRust(t *testing.T) {
	g := &model.Graph{}
	if err := Rust("testdata/rust", g, nil); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	expectLines(t, got, []string{
		"DECL struct Pedido nucleo/src/lib.rs:6 exp=true",
		"DECL method Pedido.validar nucleo/src/lib.rs:20 exp=false",
		"DECL method Repo.salvar nucleo/src/lib.rs:24 exp=true",
		"DECL macro reais nucleo/src/dinheiro.rs:5 exp=true",
		"DECL function sql_pagos api/src/pedidos/mod.rs:3 exp=true",
		"REF api/src/main.rs:4 [<module>] import -> pagar",                 // use … as marcar_pago
		"REF api/src/main.rs:10 [main] ref -> pagar",                       // alias seguido
		"REF api/src/main.rs:9 [main] ref -> criar",                        // pedidos::handlers::criar
		"REF api/src/pedidos/handlers.rs:3 [<module>] import -> sql_pagos", // super::
		"REF api/src/pedidos/handlers.rs:7 [listar] call -> Pedido.novo",   // outro crate do workspace
		"REF api/src/pedidos/handlers.rs:12 [criar] call -> zero",          // use …::*
		"REF nucleo/src/lib.rs:17 [Pedido.pago] call -> Pedido.validar",    // self.metodo()
		"REF nucleo/src/lib.rs:13 [Pedido.novo] call -> zero",              // dinheiro::zero()
		"ROUTE GET /api/pedidos handler=listar",
		"ROUTE POST /api/pedidos handler=criar",
		"ROUTE POST /api/pedidos/:id/pago handler=pagar",
		"ROUTE GET /v1/saude handler=saude",
		"CALL  /api/estoque from=criar",
		"DB table orders [select] from=sql_pagos",
	})
	if strings.Contains(got, "DECL static") {
		t.Error("&'static str não é declaração")
	}
}
