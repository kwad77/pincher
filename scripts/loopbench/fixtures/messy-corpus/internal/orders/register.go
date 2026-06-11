package orders

import (
	"context"
	"encoding/json"

	"github.com/acme/orderflow/internal/dispatch"
)

// init wires the order actions into the dispatch registry. This is the ONLY
// place the API layer's action names meet the implementing functions.
func init() {
	dispatch.Register("order.process", processAction)
	dispatch.Register("order.refund", refundAction)
}

func processAction(ctx context.Context, payload []byte) error {
	var o Order
	if err := json.Unmarshal(payload, &o); err != nil {
		return err
	}
	return ProcessOrder(ctx, &o)
}

func refundAction(ctx context.Context, payload []byte) error {
	var req RefundRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	return RefundOrder(ctx, &req)
}
