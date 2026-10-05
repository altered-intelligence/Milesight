// Package realtime provides the WebSocket hub fanning live deltas to clients
// (ADR-004: streaming-first).
package realtime

import (
	"sync"
)

// Hub routes live updates to subscribed clients per vehicle.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]struct{} // vehicleID -> clients
}

// Client is a single subscribed connection.
type Client struct {
	VehicleID string
	ch        chan Payload
}

// Payload is a single live delta delivered to a client.
type Payload struct {
	VehicleID string `json:"vehicle_id"`
	State     any    `json:"state"`
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[*Client]struct{})}
}

// Subscribe registers a client and returns its receive channel.
func (h *Hub) Subscribe(vehicleID string) (*Client, <-chan Payload) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := &Client{VehicleID: vehicleID, ch: make(chan Payload, 32)}
	if h.clients[vehicleID] == nil {
		h.clients[vehicleID] = make(map[*Client]struct{})
	}
	h.clients[vehicleID][c] = struct{}{}
	return c, c.ch
}

// Unsubscribe removes a client.
func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set := h.clients[c.VehicleID]; set != nil {
		delete(set, c)
	}
	close(c.ch)
}

// Broadcast sends a payload to every client subscribed to a vehicle.
func (h *Hub) Broadcast(vehicleID string, state any) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients[vehicleID] {
		select {
		case c.ch <- Payload{VehicleID: vehicleID, State: state}:
		default: // drop if client is slow
		}
	}
}
