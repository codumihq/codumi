package services

import javax.inject._

trait Repo {
  def todos: Seq[String]
}

class PedidoService @Inject()(repo: Repo) {
  def listar[T](n: Int): Seq[String] = {
    validar(n)
    repo.todos
  }
  private def validar(n: Int): Unit = {}
}
