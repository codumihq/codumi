defmodule LojaWeb.Router do
  use LojaWeb, :router

  pipeline :api do
    plug :accepts, ["json"]
  end

  pipeline :auth do
    plug :require_authenticated_user
  end

  scope "/", LojaWeb do
    pipe_through :browser
    live "/painel", PainelLive, :index
  end

  scope "/api", LojaWeb do
    pipe_through [:api, :auth]
    resources "/pedidos", PedidoController, only: [:index, :create] do
      get "/recibo", PedidoController, :recibo
    end
  end

  scope "/api", LojaWeb do
    pipe_through :api
    get "/saude", SaudeController, :show
  end
end
