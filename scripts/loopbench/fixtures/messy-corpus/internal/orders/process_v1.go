package orders

import (
	"context"

	"github.com/acme/orderflow/internal/queue"
	"github.com/acme/orderflow/internal/store"
)

// ProcessOrderV1 is the pre-billing-extraction pipeline, kept "temporarily"
// for rollback during the 2025 billing migration. Nothing registers or calls
// it anymore.
func ProcessOrderV1(ctx context.Context, o *Order) error {
	if err := validateOrder(o); err != nil {
		return err
	}
	rec := store.Order{ID: o.ID, Status: "pending_capture"}
	if err := store.SaveOrder(&rec); err != nil {
		return err
	}
	return queue.Publish("process_order_v1", map[string]any{"order_id": o.ID})
}
