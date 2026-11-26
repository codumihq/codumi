defmodule Loja.Vendas do
  import Ecto.Query
  alias Loja.Repo
  alias Loja.Vendas.Pedido

  def listar do
    Repo.all(from p in Pedido, order_by: p.id)
  end

  def criar(attrs) do
    %Pedido{}
    |> Pedido.changeset(attrs)
    |> Repo.insert()
  end

  def total(id), do: buscar(id).total

  defp buscar(id), do: Repo.get!(Pedido, id)
end
