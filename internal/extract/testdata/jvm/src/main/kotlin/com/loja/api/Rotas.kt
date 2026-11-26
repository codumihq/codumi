package com.loja.api

import com.loja.pedidos.PedidoRepo
import com.loja.pedidos.Pedido as P
import com.loja.pedidos.*
import retrofit2.http.GET
import retrofit2.http.POST

fun Application.rotas() {
    routing {
        route("/api") {
            post("/pedidos/{id}/pago") {
                PedidoRepo.padrao().marcarPago(call.parameters["id"]!!)
            }
            get("/saude") { call.respondText("ok") }
        }
    }
}

interface LojaApi {
    @GET("api/pedidos")
    suspend fun listar(): List<P>

    @POST("/api/pedidos/{id}/pago")
    suspend fun pagar(id: String)
}

fun montar(): String {
    val p = P("1", 2.0)
    val url = "https://loja.dev/api/estoque?x=1"
    return cliente().toString() + url + p
}
