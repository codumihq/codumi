use nucleo::{Pedido, Repo};
use nucleo::dinheiro::*;
use super::sql_pagos;

pub async fn listar() -> String {
    let _q = sql_pagos();
    let p = Pedido::novo("1");
    format!("{}", p.pago())
}

pub async fn criar() -> String {
    let z = zero();
    let c = Centavos(10);
    let _ = (z, c);
    reqwest::get("https://loja.dev/api/estoque").await;
    String::new()
}

pub async fn pagar() -> &'static str { "ok" }

#[actix_web::get("/v1/saude")]
async fn saude() -> &'static str { "ok" }

struct Memoria;
impl Repo for Memoria {
    fn salvar(&self, p: &Pedido) -> Result<(), String> { let _ = p; Ok(()) }
}
