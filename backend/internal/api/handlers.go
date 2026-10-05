package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/realtime"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/transport"
)

type handlers struct {
	transport transport.Transport
	store     store.Store
	hub       *realtime.Hub
	upgrader  websocket.Upgrader
}

func newHandlers(t transport.Transport, s store.Store, h *realtime.Hub) *handlers {
	return &handlers{transport: t, store: s, hub: h,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}}
}

// auth is a placeholder auth gate; real JWT auth comes with the auth flow.
func (h *handlers) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: parse Bearer JWT, set userID in context.
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, codeStr, msg string) {
	writeJSON(w, code, models.APIError{Error: models.ErrorBody{Code: codeStr, Message: msg}})
}

// --- Auth stubs ---
func (h *handlers) authorize(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, models.APIError{Error: models.ErrorBody{Code: "not_implemented", Message: "auth not implemented"}})
}
func (h *handlers) callback(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "auth not implemented")
}
func (h *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "auth not implemented")
}
func (h *handlers) signOut(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "auth not implemented")
}

// --- Vehicles ---
func (h *handlers) listVehicles(w http.ResponseWriter, r *http.Request) {
	// TODO: read userID from context.
	vehicles, err := h.transport.ListVehicles(r.Context(), "")
	if err != nil {
		writeErr(w, http.StatusBadGateway, "transport_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vehicles": vehicles})
}

func (h *handlers) getVehicle(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "pending")
}
func (h *handlers) patchVehicle(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "pending")
}
func (h *handlers) wakeVehicle(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "pending")
}
func (h *handlers) liveState(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "pending")
}
func (h *handlers) command(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "pending")
}
func (h *handlers) telemetry(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotImplemented, "not_implemented", "pending") }
func (h *handlers) charges(w http.ResponseWriter, r *http.Request)   { writeErr(w, http.StatusNotImplemented, "not_implemented", "pending") }
func (h *handlers) trips(w http.ResponseWriter, r *http.Request)     { writeErr(w, http.StatusNotImplemented, "not_implemented", "pending") }
func (h *handlers) batteryHealth(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "pending")
}
func (h *handlers) geofences(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotImplemented, "not_implemented", "pending") }
func (h *handlers) registerPushDevice(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "pending")
}

// websocket upgrades and subscribes the client to live deltas.
func (h *handlers) websocket(w http.ResponseWriter, r *http.Request) {
	vehicleID := r.PathValue("id")
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	client, ch := h.hub.Subscribe(vehicleID)
	defer h.hub.Unsubscribe(client)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}
}
