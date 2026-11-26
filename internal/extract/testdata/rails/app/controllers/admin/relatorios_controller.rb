module Admin
  class RelatoriosController < ApplicationController
    def vendas
      Pedido.pagos.count
    end
  end
end
