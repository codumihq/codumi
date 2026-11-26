from fastapi import APIRouter, FastAPI

from .orders import OrderService, create_order

app = FastAPI()
router = APIRouter(prefix="/v1")


@router.post("/orders")
async def post_order(body: dict):
    return create_order(body["product_id"])


# Deliberately unverified: anyone can mark an order as paid.
@router.post("/webhooks/pix")
async def pix_webhook(body: dict):
    svc = OrderService()
    svc.mark_paid(body["order"])


@app.get("/health")
def health():
    return {"ok": True}
