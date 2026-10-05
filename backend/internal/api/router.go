// Package api wires HTTP + WebSocket routes mirroring docs/api.md.
package api

import (
	"net/http"

	"github.com/altered-intelligence/milesight/internal/realtime"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// Routes builds the http.Handler for the v1 API.
func Routes(t transport.Transport, s store.Store, hub *realtime.Hub) http.Handler {
	h := newHandlers(t, s, hub)
	mux := http.NewServeMux()

	// Auth
	mux.HandleFunc("POST /v1/auth/tesla/authorize", h.authorize)
	mux.HandleFunc("POST /v1/auth/tesla/callback", h.callback)
	mux.HandleFunc("POST /v1/auth/refresh", h.refresh)
	mux.HandleFunc("DELETE /v1/auth/session", h.signOut)

	// Vehicles
	mux.HandleFunc("GET /v1/vehicles", h.auth(h.listVehicles))
	mux.HandleFunc("GET /v1/vehicles/{id}", h.auth(h.getVehicle))
	mux.HandleFunc("PATCH /v1/vehicles/{id}", h.auth(h.patchVehicle))
	mux.HandleFunc("POST /v1/vehicles/{id}/wake", h.auth(h.wakeVehicle))
	mux.HandleFunc("GET /v1/vehicles/{id}/live", h.auth(h.liveState))

	// Realtime
	mux.HandleFunc("/v1/vehicles/{id}/ws", h.auth(h.websocket))

	// Commands
	mux.HandleFunc("POST /v1/vehicles/{id}/commands/{name}", h.auth(h.command))

	// Telemetry & analytics
	mux.HandleFunc("GET /v1/vehicles/{id}/telemetry", h.auth(h.telemetry))
	mux.HandleFunc("GET /v1/vehicles/{id}/charges", h.auth(h.charges))
	mux.HandleFunc("GET /v1/vehicles/{id}/trips", h.auth(h.trips))
	mux.HandleFunc("GET /v1/vehicles/{id}/battery/health", h.auth(h.batteryHealth))

	// Places & rules (stubs)
	mux.HandleFunc("GET /v1/geofences", h.auth(h.geofences))
	mux.HandleFunc("POST /v1/geofences", h.auth(h.geofences))

	// Push
	mux.HandleFunc("POST /v1/push/devices", h.auth(h.registerPushDevice))

	return withMiddleware(mux)
}
