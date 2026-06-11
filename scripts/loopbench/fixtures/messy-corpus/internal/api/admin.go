package api

import (
	"encoding/json"
	"net/http"

	"github.com/acme/orderflow/internal/store"
)

// handleAdminImport bulk-imports historical orders. Deliberately bypasses the
// dispatch pipeline and writes straight to the store — a known backdoor that
// any SaveOrder signature change must account for.
func (s *Server) handleAdminImport(w http.ResponseWriter, r *http.Request) {
	var orders []store.Order
	if err := json.NewDecoder(r.Body).Decode(&orders); err != nil {
		http.Error(w, "bad import payload", http.StatusBadRequest)
		return
	}
	for i := range orders {
		if err := store.SaveOrder(&orders[i]); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusCreated)
}
