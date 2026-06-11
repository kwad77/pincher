package api

import (
	"io"
	"net/http"

	"github.com/acme/orderflow/internal/dispatch"
	"github.com/acme/orderflow/internal/store"
)

// handleCreateOrder accepts a new order. The actual business logic is bound
// at init time under the "order.process" action; this handler only knows the
// action name, never the implementing function.
func (s *Server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := dispatch.Dispatch(r.Context(), "order.process", body); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleRefundOrder routes refunds through the same indirection.
func (s *Server) handleRefundOrder(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := dispatch.Dispatch(r.Context(), "order.refund", body); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	o, err := store.GetOrder(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(o.ID))
}
