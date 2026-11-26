---
name: Orders
description: Order creation, stock reservation and payment marking
paths:
  - src/lib/orders.ts
  - src/app/api/orders
---

## What it does
Creates orders from the storefront, reserves stock through the `adjust_stock` RPC and marks orders as paid when a payment webhook arrives.

## Decisions
- Payment status is only changed by `markPaid`, always with the admin client: the webhook runs without a user session.
- Stock is reserved at order time, not at payment time, so two customers can't buy the last unit.

## Gotchas
- `adjust_stock` is SECURITY DEFINER and anon can still call it (see `codumi audit`). Revoke it before going live.
- The `/api/webhooks/payments` handler has no signature check yet; only the Stripe one is verified.

## Pending
- Release reserved stock when an order expires unpaid.
