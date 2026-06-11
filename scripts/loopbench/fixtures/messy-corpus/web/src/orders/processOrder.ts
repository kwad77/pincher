// Client-side order submission: optimistic cart state + POST to the Go API.
// Same name as the Go pipeline and the Python fulfillment handler; THIS one
// only manages browser state and the HTTP call.
import { createOrder } from "../api/client";
import { cartToPayload, markCartSubmitting, clearCart, markCartFailed } from "../state/cart";

export async function processOrder(): Promise<void> {
  markCartSubmitting();
  try {
    await createOrder(cartToPayload());
    clearCart();
  } catch (err) {
    markCartFailed(String(err));
    throw err;
  }
}
