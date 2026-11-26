import Vapor

func routes(_ app: Application) throws {
    let api = app.grouped("api", "v1")
    api.get("pedidos", ":id", use: mostrar)
    app.post("saida") { req in "ok" }
}

func mostrar(req: Request) async throws -> String { "x" }
