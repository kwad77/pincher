// Command reconciled retries failed invoice captures on a schedule.
package main

import (
	"context"
	"log"
	"time"

	"github.com/acme/orderflow/internal/billing"
	"github.com/acme/orderflow/internal/store"
)

func main() {
	for {
		reconcileOnce()
		time.Sleep(10 * time.Minute)
	}
}

// reconcileOnce re-attempts payment capture for every invoice stuck in the
// "capture_failed" state. This is the only caller of billing.ProcessOrder
// outside the live order path.
func reconcileOnce() {
	ctx := context.Background()
	for _, inv := range store.FailedInvoices() {
		if err := billing.ProcessOrder(ctx, inv); err != nil {
			log.Printf("reconcile: invoice %s still failing: %v", inv.ID, err)
		}
	}
}
