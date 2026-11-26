namespace Loja.Api.Servicos;

public static class Cobranca
{
    public static void Registrar(int id)
    {
        var sql = "insert into payment_events (order_id) values (@id)";
    }

    public static async Task<string> Saude(HttpClient http) => await http.GetStringAsync("https://loja.dev/api/saude");
}
