package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestORM(t *testing.T) {
	g := &model.Graph{}
	g.Decls = []model.Decl{
		{File: "src/servico.ts", Name: "listar", Line: 2}, {File: "src/servico.ts", Name: "criar", Line: 8},
		{File: "loja/views.py", Name: "listar", Line: 3}, {File: "api/db.py", Name: "buscar", Line: 4},
	}
	ORM("testdata/orm", g, nil)
	got := dumpGraph(g)
	expectLines(t, got, []string{
		"DB table pedidos [select] from=listar",
		"DB table itens_pedido [select] from=listar",
		"DB table itens_pedido [insert] from=listar",
		"DB table Cliente [insert] from=criar",
		"DB table loja_pedido [update] from=listar",
		"DB table clientes [select] from=listar",
		"DB table produtos [select] from=buscar",
	})
}
