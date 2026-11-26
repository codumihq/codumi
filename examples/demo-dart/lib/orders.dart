import 'api.dart';
import 'supabase.dart';

final db = SupabaseClient();

class OrderService {
  Future<void> createOrder(String productId) async {
    db.from('orders').insert({'product_id': productId}).select();
    await db.rpc('adjust_stock', params: {'p_product': productId, 'p_delta': -1});
    await Api.post('/v1/orders', {'product_id': productId});
  }

  Future<void> attachReceipt(String orderId, List<int> pdf) async {
    await db.storage.from('receipts').upload('$orderId/receipt.pdf', pdf);
    await Api.getChecked('/v1/orders/$orderId');
  }
}
