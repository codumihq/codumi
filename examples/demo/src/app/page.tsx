import { OrderButton } from '@/components/OrderButton'

function Hero() {
  return (
    <section>
      <h1>Fresh coffee, delivered</h1>
      <OrderButton productId="espresso" />
    </section>
  )
}

export default function Home() {
  return (
    <main>
      <Hero />
    </main>
  )
}
