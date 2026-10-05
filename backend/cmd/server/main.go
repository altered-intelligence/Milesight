// Command server runs the Milesight backend API.
package main

import (
	"log"
	"net/http"

	"github.com/altered-intelligence/milesight/internal/api"
	"github.com/altered-intelligence/milesight/internal/config"
	"github.com/altered-intelligence/milesight/internal/realtime"
	"github.com/altered-intelligence/milesight/internal/store/postgres"
	"github.com/altered-intelligence/milesight/internal/transport"
	"github.com/altered-intelligence/milesight/internal/transport/fleet"
	"github.com/altered-intelligence/milesight/internal/transport/owner"
)

func main() {
	cfg := config.Load()

	st, err := postgres.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	var t transport.Transport
	switch cfg.Transport {
	case "fleet":
		t, err = fleet.New(nil)
	default: // owner
		t, err = owner.New(nil)
	}
	if err != nil {
		log.Fatalf("transport: %v", err)
	}

	hub := realtime.NewHub()
	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: api.Routes(t, st, hub),
	}

	log.Printf("milesight listening on %s (transport=%s)", cfg.Addr, cfg.Transport)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
