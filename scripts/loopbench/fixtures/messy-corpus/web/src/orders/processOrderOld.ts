// DEAD CODE: pre-registry checkout flow, superseded by orders/actions.ts.
// Nothing imports this module; kept while the redesign A/B test winds down.
import { createOrder } from "../api/client";
import { cartToPayload } from "../state/cart";

export async function processOrder(): Promise<void> {
  // Old behaviour: fire-and-forget, no optimistic state, alert() on error.
  try {
    await createOrder(cartToPayload());
  } catch (err) {
    alert(`order failed: ${err}`);
  }
}
