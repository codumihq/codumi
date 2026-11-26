using Loja.Api.Servicos;

var app = WebApplication.CreateBuilder(args).Build();
app.MapGet("/health", () => "ok");
app.MapPost("/api/cobranca/{id}", Cobranca.Registrar);
app.Run();
