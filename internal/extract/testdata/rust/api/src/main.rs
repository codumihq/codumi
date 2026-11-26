mod pedidos;

use axum::{routing::{get, post}, Router};
use crate::pedidos::handlers::{listar, pagar as marcar_pago};

#[tokio::main]
async fn main() {
    let app = Router::new()
        .route("/api/pedidos", get(listar).post(pedidos::handlers::criar))
        .route("/api/pedidos/:id/pago", post(marcar_pago));
    let _ = app;
}
