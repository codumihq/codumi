//! Regras de negócio compartilhadas.
pub mod dinheiro;

/// Um pedido.
#[derive(Debug, Clone)]
pub struct Pedido {
    pub id: String,
    pub total: dinheiro::Centavos,
}

impl Pedido {
    pub fn novo(id: &str) -> Self {
        Pedido { id: id.to_string(), total: dinheiro::zero() }
    }

    pub fn pago(&self) -> bool {
        self.validar() && self.total.0 > 0
    }

    fn validar(&self) -> bool { !self.id.is_empty() }
}

pub trait Repo {
    fn salvar(&self, p: &Pedido) -> Result<(), String>;
}
