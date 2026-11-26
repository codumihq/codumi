pub mod handlers;

pub(crate) fn sql_pagos() -> &'static str {
    r#"SELECT id, total FROM orders WHERE status = 'paid'"#
}
