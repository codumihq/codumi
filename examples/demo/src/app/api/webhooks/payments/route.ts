import { markPaid } from '@/lib/orders'

// Payment provider callback. (Deliberately unverified for the demo:
// there is no signature check, so anyone can mark an order as paid.)
export async function POST(req: Request) {
  const event = await req.json()
  if (event.type === 'payment.succeeded') await markPaid(event.orderId)
  return new Response('ok')
}
