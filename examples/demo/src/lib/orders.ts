import { createClient, supabaseAdmin } from '@/lib/supabase'

export type NewOrder = { productId: string; quantity: number }

export async function createOrder(order: NewOrder) {
  const db = createClient()
  const { data } = await db.from('orders').insert({ product_id: order.productId, quantity: order.quantity }).select().single()
  await reserveStock(order.productId, order.quantity)
  return data
}

async function reserveStock(productId: string, quantity: number) {
  await createClient().rpc('adjust_stock', { p_product: productId, p_delta: -quantity })
}

export async function markPaid(orderId: string) {
  await supabaseAdmin().from('orders').update({ status: 'paid' }).eq('id', orderId)
  await supabaseAdmin().from('audit_logs').insert({ action: 'order.paid', order_id: orderId })
}

export async function uploadAttachment(orderId: string, file: Blob) {
  await createClient().storage.from('attachments').upload(`${orderId}/receipt.pdf`, file)
}
