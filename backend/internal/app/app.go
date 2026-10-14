// Package app wires configuration into a running Milesight server. It is shared
// by cmd/server and the end-to-end tests so both exercise the same graph.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/altered-intelligence/milesight/internal/api"
	"github.com/altered-intelligence/milesight/internal/auth"
	"github.com/altered-intelligence/milesight/internal/commands"
	"github.com/altered-intelligence/milesight/internal/config"
	"github.com/altered-intelligence/milesight/internal/ingest"
	"github.com/altered-intelligence/milesight/internal/notify"
	"github.com/altered-intelligence/milesight/internal/realtime"
	"github.com/altered-intelligence/milesight/internal/sim"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/store/memory"
	"github.com/altered-intelligence/milesight/internal/store/postgres"
	"github.com/altered-intelligence/milesight/internal/tesla"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// App is the assembled server.
type App struct {
	Cfg     config.Config
	Store   store.Store
	Ingest  *ingest.Manager
	Hub     *realtime.Hub
	API     *api.Server
	Handler http.Handler
	Log     *slog.Logger
}

// Options allow tests to inject a clock and store.
type Options struct {
	Version string
	Log     *slog.Logger
	Store   store.Store
	Now     func() time.Time
}

// New builds the application from configuration.
func New(ctx context.Context, cfg config.Config, o Options) (*App, error) {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	st := o.Store
	if st == nil {
		switch cfg.Store {
		case "postgres":
			pg, err := postgres.New(ctx, cfg.DatabaseURL)
			if err != nil {
				return nil, err
			}
			st = pg
		default:
			st = memory.New()
		}
	}
	if err := st.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	cipher, err := auth.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}

	oauth := map[string]*tesla.OAuth{}
	var fleetOAuth, ownerOAuth *tesla.OAuth
	if cfg.FleetEnabled() {
		fleetOAuth = tesla.NewFleetOAuth(nil, cfg.Tesla.ClientID, cfg.Tesla.ClientSecret, cfg.Tesla.Scopes)
		oauth["fleet"] = fleetOAuth
	}
	if cfg.OwnerLogin || cfg.Transport == "owner" {
		ownerOAuth = tesla.NewOwnerOAuth(nil)
		oauth["owner"] = ownerOAuth
	}
	creds := &tesla.Credentials{Store: st, Cipher: cipher, OAuth: oauth, Now: o.Now}

	reg := transport.Registry{}
	if cfg.DevLogin || cfg.Transport == "sim" {
		reg["sim"] = sim.New(sim.Options{HomeLat: cfg.Sim.HomeLat, HomeLon: cfg.Sim.HomeLon, Vehicles: cfg.Sim.Vehicles, Now: o.Now})
	}
	if fleetOAuth != nil {
		ft, err := tesla.NewFleetTransport(creds, cfg.Tesla.CommandProxyURL, cfg.Tesla.CommandProxyCA)
		if err != nil {
			return nil, err
		}
		reg["fleet"] = ft
	}
	if ownerOAuth != nil {
		reg["owner"] = tesla.NewOwnerTransport(creds)
	}

	var pusher notify.Pusher = notify.LogPusher{Log: log}
	if cfg.APNs.Enabled() {
		ap, err := notify.NewAPNs(cfg.APNs.KeyFile, cfg.APNs.KeyID, cfg.APNs.TeamID, cfg.APNs.Topic)
		if err != nil {
			return nil, err
		}
		pusher = ap
	}

	hub := realtime.NewHub()
	mgr := &ingest.Manager{
		Store: st, Transports: reg, Hub: hub, Log: log, HistoryDays: cfg.Sim.HistoryDays, Now: o.Now,
		Dispatcher: &notify.Dispatcher{Store: st, Pusher: pusher, Log: log, Now: o.Now},
	}
	signer := auth.NewSigner(cfg.JWTSecret)
	srv := &api.Server{
		Cfg: cfg, Store: st, Creds: creds, FleetOAuth: fleetOAuth, OwnerOAuth: ownerOAuth,
		Sessions:   &auth.Sessions{Store: st, Signer: signer, AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL, Now: o.Now},
		Ingest:     mgr,
		Commands:   &commands.Service{Store: st, Transports: reg, Poker: mgr, Log: log, Now: o.Now},
		Hub:        hub,
		Transports: reg,
		Log:        log,
		Version:    o.Version,
		Now:        o.Now,
	}
	return &App{Cfg: cfg, Store: st, Ingest: mgr, Hub: hub, API: srv, Handler: srv.Handler(), Log: log}, nil
}
