// Checkout button -> dispatch("order/submit"). The button never references
// processOrder directly; the registry resolves it at click time.
import { dispatch } from "../registry";

export function wireCheckout(btn: HTMLElement | null): void {
  if (!btn) return;
  btn.addEventListener("click", () => {
    void dispatch("order/submit", {});
  });
}
