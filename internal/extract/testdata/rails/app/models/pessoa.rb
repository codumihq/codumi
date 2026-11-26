module Cadastro
  class Pessoa < ApplicationRecord
    self.table_name = "clientes"
  end
end
