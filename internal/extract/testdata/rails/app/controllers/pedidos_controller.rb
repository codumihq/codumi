class PedidosController < ApplicationController
  before_action :carregar, only: [:show]
  skip_before_action :authenticate_user!, only: [:index]

  def index
    @pedidos = Pedido.recentes
  end

  def show
  end

  def create
    pedido = Pedido.create!(params.require(:pedido))
    Cobranca::Gerar.new(pedido).call
  end

  def pagar
  end

  private

  def carregar
    @pedido = Pedido.find(params[:id])
  end
end
