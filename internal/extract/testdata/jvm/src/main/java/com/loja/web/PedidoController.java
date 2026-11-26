package com.loja.web;

import com.loja.pedidos.PedidoRepo;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/v1/pedidos")
public class PedidoController {
    private final PedidoRepo repo = PedidoRepo.Companion.padrao();

    @GetMapping("/{id}")
    public String buscar(@PathVariable String id) {
        String sql = "select * from orders where id = ?";
        return formatar(id) + sql;
    }

    @PostMapping(value = "/{id}/cancelar")
    public void cancelar(String id) throws Exception {
        Auditoria.registrar("cancelar", id);
    }

    private String formatar(String id) { return "#" + id; }
}

class Auditoria {
    static void registrar(String acao, String id) { }
}
