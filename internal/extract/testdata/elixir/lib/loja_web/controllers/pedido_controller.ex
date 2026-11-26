defmodule LojaWeb.PedidoController do
  use LojaWeb, :controller
  alias Loja.Vendas

  def index(conn, _params) do
    json(conn, Vendas.listar())
  end

  def create(conn, params) do
    {:ok, p} = Vendas.criar(params)
    HTTPoison.post("https://x.com/api/estoque", "")
    json(conn, p)
  end
end
