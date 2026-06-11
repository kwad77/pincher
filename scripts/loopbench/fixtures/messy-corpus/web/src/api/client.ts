// Thin HTTP client for the Go orderd service.

export interface OrderPayload {
  id: string;
  skus: string[];
  total_cents: number;
  email: string;
}

export async function createOrder(payload: OrderPayload): Promise<void> {
  const res = await fetch("/api/v1/orders", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) {
    throw new Error(`createOrder failed: ${res.status}`);
  }
}

export async function refundOrder(orderId: string, reason: string): Promise<void> {
  const res = await fetch("/api/v1/orders/refund", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ order_id: orderId, reason }),
  });
  if (!res.ok) {
    throw new Error(`refundOrder failed: ${res.status}`);
  }
}
