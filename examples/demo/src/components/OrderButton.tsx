'use client'

import { Button } from '@/components/ui/button'

export function OrderButton({ productId }: { productId: string }) {
  async function order() {
    await fetch('/api/orders', { method: 'POST', body: JSON.stringify({ productId, quantity: 1 }) })
  }
  return <Button onClick={order}>Order now</Button>
}
