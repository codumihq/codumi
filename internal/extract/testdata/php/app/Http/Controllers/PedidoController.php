<?php

namespace App\Http\Controllers;

use App\Models\Pedido;
use App\Models\ItemPedido as Item;
use App\Services\Cobranca;
use function App\Services\formatar;

class PedidoController extends Controller
{
    // # não é comentário dentro de texto: '#1'
    public function show(Pedido $pedido)
    {
        $itens = Item::where('pedido_id', $pedido->id)->get();
        return formatar($pedido->total) . '#' . count($itens);
    }

    public function pagar(int $id, Cobranca $c)
    {
        $c->pagar($id);
        (new Cobranca())->pagar($id);
        return response()->noContent();
    }
}
