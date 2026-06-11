// Command orderd runs the orderflow HTTP API service.
package main

import (
	"log"
	"net/http"

	"github.com/acme/orderflow/internal/api"
	_ "github.com/acme/orderflow/internal/orders" // action registrations (init)
)

func main() {
	srv := api.NewServer()
	log.Println("orderd listening on :8080")
	if err := http.ListenAndServe(":8080", srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
