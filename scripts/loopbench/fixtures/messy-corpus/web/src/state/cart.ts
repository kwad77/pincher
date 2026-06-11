import type { OrderPayload } from "../api/client";

interface CartState {
  items: { sku: string; cents: number }[];
  email: string;
  status: "idle" | "submitting" | "failed";
  error?: string;
}

const state: CartState = { items: [], email: "", status: "idle" };

export function addItem(sku: string, cents: number): void {
  state.items.push({ sku, cents });
}

export function cartToPayload(): OrderPayload {
  return {
    id: `web-${Date.now()}`,
    skus: state.items.map((i) => i.sku),
    total_cents: state.items.reduce((sum, i) => sum + i.cents, 0),
    email: state.email,
  };
}

export function markCartSubmitting(): void {
  state.status = "submitting";
}

export function markCartFailed(error: string): void {
  state.status = "failed";
  state.error = error;
}

export function clearCart(): void {
  state.items = [];
  state.status = "idle";
}
