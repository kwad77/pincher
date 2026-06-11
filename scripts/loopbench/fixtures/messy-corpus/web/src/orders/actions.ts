// Registers order-related UI actions. Imported for side effects from main.ts.
import { registerAction } from "../registry";
import { processOrder } from "./processOrder";
import { refundOrder } from "../api/client";

registerAction("order/submit", async () => {
  await processOrder();
});

registerAction("order/refund", async (payload) => {
  const p = payload as { orderId: string; reason: string };
  await refundOrder(p.orderId, p.reason);
});
