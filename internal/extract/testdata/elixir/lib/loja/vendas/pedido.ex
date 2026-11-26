defmodule Loja.Vendas.Pedido do
  use Ecto.Schema
  import Ecto.Changeset

  schema "pedidos" do
    field :total, :decimal
  end

  def changeset(pedido, attrs) do
    pedido |> cast(attrs, [:total])
  end
end
