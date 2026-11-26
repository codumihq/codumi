package controllers

import services.{PedidoService, Repo => R}
import play.api.mvc._

class Pedidos(service: PedidoService) {
  def index() = Action {
    service.listar(1)
    Ok("x")
  }
}
