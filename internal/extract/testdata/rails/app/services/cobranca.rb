module Cobranca
  class Gerar
    def initialize(pedido)
      @pedido = pedido
    end

    def call
      Pedido.where(id: @pedido.id).update_all(cobrado: true)
      Cadastro::Pessoa.find_by(email: "x@y.z")
      registrar
      HTTParty.post("https://loja.com/api/estoque")
    end

    def registrar
      ActiveRecord::Base.connection.execute("UPDATE caixas SET total = 0")
    end

    def avulso
      g = Gerar.new(nil)
      g.registrar
    end
  end
end
