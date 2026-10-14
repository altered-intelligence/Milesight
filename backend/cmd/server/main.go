// Command server runs the Milesight backend API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/altered-intelligence/milesight/internal/app"
	"github.com/altered-intelligence/milesight/internal/config"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	cfg, err := config.Load()
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	if cfg.Insecure {
		log.Warn("running with development secrets (JWT_SECRET / MILESIGHT_ENCRYPTION_KEY); never use this in production")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, app.Options{Version: version, Log: log})
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer a.Store.Close()

	go a.Ingest.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: WebSockets and wake-then-command requests are long-lived;
		// handlers bound their own work with contexts.
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("milesight listening", "addr", cfg.Addr, "transport", cfg.Transport, "store", cfg.Store,
		"dev_login", cfg.DevLogin, "fleet_oauth", cfg.FleetEnabled(), "apns", cfg.APNs.Enabled(), "version", version)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
	log.Info("milesight stopped")
}
