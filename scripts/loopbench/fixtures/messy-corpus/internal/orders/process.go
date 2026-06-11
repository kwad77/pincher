package orders

import (
	"context"
	"fmt"

	"github.com/acme/orderflow/internal/billing"
	"github.com/acme/orderflow/internal/queue"
	"github.com/acme/orderflow/internal/store"
)

// Order is an inbound customer order.
type Order struct {
	ID    string   `json:"id"`
	SKUs  []string `json:"skus"`
	Total int64    `json:"total_cents"`
	Email string   `json:"email"`
}

// ProcessOrder is the LIVE order pipeline: validate, capture payment,
// persist, then enqueue fulfillment for the Python workers.
//
// Not to be confused with billing.ProcessOrder (payment capture only) or the
// frontend/worker functions of the same name.
func ProcessOrder(ctx context.Context, o *Order) error {
	if err := validateOrder(o); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	inv := store.Invoice{ID: "inv-" + o.ID, OrderID: o.ID, AmountCents: o.Total}
	if err := billing.ProcessOrder(ctx, inv); err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	rec := store.Order{ID: o.ID, Status: "paid"}
	if err := store.SaveOrder(&rec); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	return queue.Publish("process_order", map[string]any{
		"order_id": o.ID,
		"skus":     o.SKUs,
		"email":    o.Email,
	})
}
