import 'orders.dart';

Future<void> main() async {
  final orders = OrderService();
  await orders.createOrder('p1');
  await orders.createOrder('p2');
  await orders.attachReceipt('o1', const [1, 2, 3]);
}
