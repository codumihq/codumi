import requests

from .db import conn, supabase


def create_order(product_id):
    supabase.table("orders").insert({"product_id": product_id}).execute()
    supabase.rpc("adjust_stock", {"p_product": product_id, "p_delta": -1}).execute()
    return product_id


class OrderService:
    def mark_paid(self, order_id):
        conn.execute("UPDATE orders SET status = 'paid' WHERE id = %s", (order_id,))
        self.notify(order_id)

    def notify(self, order_id):
        requests.post(f"https://api.example.com/v1/notifications/{order_id}")
