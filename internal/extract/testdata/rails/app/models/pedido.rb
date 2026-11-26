class Pedido < ApplicationRecord
  has_many :itens
  before_save :normalizar
  scope :pagos, -> { where(pago: true) }

  def self.recentes
    where("created_at > ?", 1.day.ago).order(:id)
  end

  def total
    itens.sum(:valor)
  end

  private

  def normalizar
    self.codigo = codigo.strip if codigo
  end
end
