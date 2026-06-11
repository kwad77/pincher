package billing

import (
	"context"
	"errors"
	"hash/fnv"
)

// chargeCard is a stub gateway call; fails deterministically for unlucky ids.
func chargeCard(_ context.Context, invoiceID string, amountCents int64) error {
	if amountCents <= 0 {
		return errors.New("gateway: zero-amount capture")
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(invoiceID))
	if h.Sum32()%97 == 0 {
		return errors.New("gateway: card declined")
	}
	return nil
}

func refundCard(_ context.Context, orderID string) error {
	if orderID == "" {
		return errors.New("gateway: missing order id")
	}
	return nil
}
