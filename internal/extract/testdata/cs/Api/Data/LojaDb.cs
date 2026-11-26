using Microsoft.EntityFrameworkCore;

namespace Loja.Api.Data;

[Table("orders")]
public class Pedido
{
    public int Id { get; set; }
    public string Status { get; set; } = "";
}

public class LojaDb : DbContext
{
    public DbSet<Pedido> Pedidos { get; set; }
    public DbSet<Cliente> Clientes => Set<Cliente>();
}

public record Cliente(int Id, string Nome);
