// Package api exposes the orderflow HTTP surface.
package api

import "net/http"

// Server owns the HTTP routing table.
type Server struct {
	mux *http.ServeMux
}

// NewServer builds the routing table. Handlers delegate business logic
// through the dispatch registry; nothing here calls domain packages directly.
func NewServer() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /api/v1/orders", s.handleCreateOrder)
	s.mux.HandleFunc("POST /api/v1/orders/refund", s.handleRefundOrder)
	s.mux.HandleFunc("GET /api/v1/orders/{id}", s.handleGetOrder)
	s.mux.HandleFunc("POST /api/v1/admin/import", s.handleAdminImport)
	return s
}

// Routes returns the underlying handler for http.ListenAndServe.
func (s *Server) Routes() http.Handler { return s.mux }
