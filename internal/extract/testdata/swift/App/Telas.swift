import SwiftUI

struct PedidosView: View {
    let service = PedidoService()
    var body: some View {
        List { Linha() }
            .task { try? await service.listar() }
    }
}

struct Linha: View {
    var body: some View { Text("x") }
}
