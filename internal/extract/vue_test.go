package extract

import (
	"testing"

	"github.com/codumihq/codumi/internal/model"
)

func TestVueNuxt(t *testing.T) {
	g := &model.Graph{}
	g.Decls = append(g.Decls, model.Decl{File: "src/components/util.ts", Name: "formatar", Kind: "function", Line: 1, Exported: true})
	if err := SFC("testdata/vue", g); err != nil {
		t.Fatal(err)
	}
	got := dumpGraph(g)
	for _, p := range g.Pages {
		got += "PAGE " + p.Path + " " + p.Component + " " + p.File + "\n"
	}
	expectLines(t, got, []string{
		"PAGE / Home src/views/Home.vue",
		"PAGE /pedidos Pedidos src/views/Pedidos.vue",
		"PAGE /pedidos/:id Pedido src/views/Pedido.vue",
		"REF src/views/Home.vue:2 [<module>] import -> BaseBotao",
		"REF src/views/Home.vue:4 [Home] call -> formatar",
		"REF src/views/Home.vue:8 [Home] jsx -> BaseBotao",
		"REF src/views/Home.vue:8 [Home] call -> formatar",
		"REF src/views/Home.vue:9 [Home] jsx -> BaseBotao",
		"REF src/views/Home.vue:10 [Home] jsx -> Cartao",
	})

	g = &model.Graph{}
	if err := SFC("testdata/nuxt", g); err != nil {
		t.Fatal(err)
	}
	got = dumpGraph(g)
	for _, p := range g.Pages {
		got += "PAGE " + p.Path + " " + p.Component + "\n"
	}
	expectLines(t, got, []string{
		"PAGE /pedidos/[id] [id]",
		"PAGE / index",
		"REF pages/pedidos/[id].vue:4 [[id]] jsx -> Cartao",
		"CALL  /api/pedidos/1 from=[id]",
		"ROUTE GET /api/pedidos handler=default",
		"ROUTE POST /api/pedidos/[id] handler=default",
		"ROUTE ANY /api/saude handler=default",
	})
}
