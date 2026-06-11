import { dispatch } from "../registry";

export function wireRefundForm(form: HTMLFormElement | null): void {
  if (!form) return;
  form.addEventListener("submit", (ev) => {
    ev.preventDefault();
    const data = new FormData(form);
    void dispatch("order/refund", {
      orderId: String(data.get("order_id") ?? ""),
      reason: String(data.get("reason") ?? ""),
    });
  });
}
