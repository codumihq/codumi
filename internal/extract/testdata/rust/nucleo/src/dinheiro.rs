pub struct Centavos(pub i64);

pub fn zero() -> Centavos { Centavos(0) }

macro_rules! reais {
    ($v:expr) => { Centavos($v * 100) };
}
