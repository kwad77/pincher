// Package billing talks to the payment gateway.
package billing

import (
	"context"
	"fmt"

	"github.com/acme/orderflow/internal/config"
	"github.com/acme/orderflow/internal/store"
)

// ProcessOrder captures payment for one invoice. Despite the name collision
// with orders.ProcessOrder (the full pipeline), this function ONLY charges
// the card, retrying up to the order_retry_limit config knob.
func ProcessOrder(ctx context.Context, inv store.Invoice) error {
	limit := config.OrderRetryLimit()
	var err error
	for attempt := 1; attempt <= limit; attempt++ {
		if err = chargeCard(ctx, inv.ID, inv.AmountCents); err == nil {
			return nil
		}
	}
	return fmt.Errorf("capture failed after %d attempts: %w", limit, err)
}

// RefundPayment reverses a prior capture.
func RefundPayment(ctx context.Context, orderID string) error {
	return refundCard(ctx, orderID)
}
