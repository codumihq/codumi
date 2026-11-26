using Loja.Api.Data;
using Loja.Api.Servicos;
using Microsoft.AspNetCore.Mvc;

namespace Loja.Api.Controllers
{
    [ApiController]
    [Route("api/[controller]"), Authorize]
    public class PedidosController : ControllerBase
    {
        private readonly LojaDb _db;
        public PedidosController(LojaDb db) { _db = db; }

        [AllowAnonymous, HttpGet("{id}")]
        public async Task<IActionResult> Buscar(int id)
        {
            var p = await _db.Pedidos.FindAsync(id);
            return Ok(Formatar(p));
        }

        [HttpPost("{id}/pagar")]
        public IActionResult Pagar(int id)
        {
            Cobranca.Registrar(id);
            _db.Pedidos.Update(new Pedido { Id = id, Status = "paid" });
            return NoContent();
        }

        private string Formatar(Pedido p) => $"#{p.Id}";
    }
}
