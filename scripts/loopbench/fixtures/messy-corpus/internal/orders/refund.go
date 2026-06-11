package orders

import (
	"context"
	"fmt"

	"github.com/acme/orderflow/internal/billing"
	"github.com/acme/orderflow/internal/queue"
	"github.com/acme/orderflow/internal/store"
)

// RefundRequest asks for a full refund of an existing order.
type RefundRequest struct {
	OrderID string `json:"order_id"`
	Reason  string `json:"reason"`
}

// RefundOrder reverses payment and tells the workers to claw back fulfillment.
func RefundOrder(ctx context.Context, req *RefundRequest) error {
	o, err := store.GetOrder(req.OrderID)
	if err != nil {
		return fmt.Errorf("lookup: %w", err)
	}
	if err := billing.RefundPayment(ctx, o.ID); err != nil {
		return fmt.Errorf("refund: %w", err)
	}
	o.Status = "refunded"
	if err := store.SaveOrder(o); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	return queue.Publish("process_refund", map[string]any{
		"order_id": req.OrderID,
		"reason":   req.Reason,
	})
}
