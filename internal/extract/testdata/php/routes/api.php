<?php

use App\Http\Controllers\PedidoController;
use Illuminate\Support\Facades\Route;

Route::get('/pedidos/{pedido}', [PedidoController::class, 'show']);
Route::middleware('auth:sanctum')->prefix('admin')->group(function () {
    Route::post('/pedidos/{id}/pagar', [PedidoController::class, 'pagar']);
});
Route::apiResource('clientes', PedidoController::class);
Route::get('/saude', fn () => 'ok');
