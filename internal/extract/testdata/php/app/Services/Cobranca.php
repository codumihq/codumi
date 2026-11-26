<?php
namespace App\Services;

use App\Models\Pedido;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Http;

class Cobranca
{
    public function pagar(int $id): void
    {
        Pedido::where('id', $id)->update(['status' => 'paid']);
        DB::table('payment_events')->insert(['order_id' => $id]);
        $this->avisar($id);
    }

    private function avisar(int $id): void
    {
        Http::post('https://loja.dev/api/avisos', ['id' => $id]);
        $sql = 'select count(*) from orders where status = ?';
    }
}

function formatar($v) { return 'R$ ' . $v; }
