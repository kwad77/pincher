package orders

import (
	"errors"
	"strings"
)

func validateOrder(o *Order) error {
	if o.ID == "" {
		return errors.New("missing order id")
	}
	if len(o.SKUs) == 0 {
		return errors.New("empty order")
	}
	if o.Total <= 0 {
		return errors.New("non-positive total")
	}
	for i, s := range o.SKUs {
		o.SKUs[i] = normalizeSKU(s)
	}
	return nil
}

func normalizeSKU(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}
