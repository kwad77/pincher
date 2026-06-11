// Package store is an in-memory stand-in for the orders database.
package store

import (
	"fmt"
	"sync"
)

// Order is the persisted order record.
type Order struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Invoice is a payment capture attempt against an order.
type Invoice struct {
	ID          string
	OrderID     string
	AmountCents int64
	State       string
}

var (
	mu     sync.RWMutex
	orders = map[string]*Order{}
)

// SaveOrder upserts one order record.
func SaveOrder(o *Order) error {
	if o.ID == "" {
		return fmt.Errorf("store: order missing id")
	}
	mu.Lock()
	defer mu.Unlock()
	orders[o.ID] = o
	return nil
}

// GetOrder fetches one order by id.
func GetOrder(id string) (*Order, error) {
	mu.RLock()
	defer mu.RUnlock()
	o, ok := orders[id]
	if !ok {
		return nil, fmt.Errorf("store: no order %q", id)
	}
	return o, nil
}

// FailedInvoices lists invoices stuck in capture_failed (stub).
func FailedInvoices() []Invoice {
	return nil
}
