package com.loja.pedidos

import io.github.jan.supabase.SupabaseClient

/* comentário /* aninhado */ ainda comentário */
data class Pedido(val id: String, val total: Double)

class PedidoRepo(private val supabase: SupabaseClient) {
    suspend fun marcarPago(id: String) {
        supabase.from("orders").update(mapOf("status" to "paid")) { filter { eq("id", id) } }
        registrar(id)
    }

    private fun registrar(id: String) = println("pago $id")

    companion object {
        fun padrao(): PedidoRepo = PedidoRepo(cliente())
    }
}

fun cliente(): SupabaseClient = TODO()

fun String.semEspacos(): String = this.trim()
