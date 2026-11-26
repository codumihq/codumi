import SwiftUI

struct Pedido: Codable {
    let id: Int
    func total() -> Double { 0 }
}

final class PedidoService {
    func listar() async throws -> [Pedido] {
        let url = URL(string: "https://loja.com/api/pedidos")!
        _ = try await supabase.from("pedidos").select().execute()
        return []
    }

    func salvar(_ p: Pedido) {
        self.listarCache()
        log(p.total())
    }
}

extension PedidoService {
    func listarCache() {}
}

func log(_ v: Double) {}
