import { markPaid } from '@/lib/orders'

function verifySignature(payload: string, header: string | null): boolean {
  return !!header && payload.length > 0 // placeholder for a real HMAC check
}

export async function POST(req: Request) {
  const payload = await req.text()
  if (!verifySignature(payload, req.headers.get('stripe-signature'))) return new Response('bad signature', { status: 400 })
  const event = JSON.parse(payload)
  await markPaid(event.data.object.metadata.orderId)
  return new Response('ok')
}
