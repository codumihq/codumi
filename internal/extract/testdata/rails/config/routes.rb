Rails.application.routes.draw do
  root "pedidos#index"
  resources :pedidos, only: [:index, :show, :create] do
    member do
      post :pagar
    end
    resources :itens, only: :index
  end
  namespace :admin do
    get "vendas", to: "relatorios#vendas"
  end
  scope "/api/v1" do
    get "saude" => "saude#ver"
  end
  if Rails.env.development?
    get "debug", to: "debug#ver"
  end
  delete "/sair", to: "sessoes#destroy"
end
