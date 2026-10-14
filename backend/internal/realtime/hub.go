// Package realtime fans live vehicle updates out to WebSocket subscribers
// (ADR-004: streaming-first). It also tells ingestion which cars are being
// watched so it can poll them faster.
package realtime

import (
	"sync"
	"time"
)

// Message types sent to clients.
const (
	TypeState   = "state"   // data: models.LiveState
	TypeEvent   = "event"   // data: models.Event
	TypeCommand = "command" // data: models.CommandResult
	TypeTrip    = "trip"    // data: models.Trip (in progress or just ended)
	TypeCharge  = "charge"  // data: models.ChargeSession
)

// Message is one envelope on the wire: {"type": ..., "vehicle_id": ..., "data": ...}.
type Message struct {
	Type      string    `json:"type"`
	VehicleID string    `json:"vehicle_id"`
	At        time.Time `json:"at"`
	Data      any       `json:"data"`
}

// Client is one subscription.
type Client struct {
	vehicleID string
	ch        chan Message
	once      sync.Once
}

// C returns the receive channel; it is closed when the client is unsubscribed
// or falls too far behind.
func (c *Client) C() <-chan Message { return c.ch }

// Hub routes messages to subscribers of each vehicle.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*Client]struct{}
	viewed  map[string]time.Time // last REST read of a vehicle's live state
	now     func() time.Time
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{clients: map[string]map[*Client]struct{}{}, viewed: map[string]time.Time{}, now: time.Now}
}

// Subscribe registers a client for vehicleID.
func (h *Hub) Subscribe(vehicleID string) *Client {
	c := &Client{vehicleID: vehicleID, ch: make(chan Message, 64)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[vehicleID] == nil {
		h.clients[vehicleID] = map[*Client]struct{}{}
	}
	h.clients[vehicleID][c] = struct{}{}
	return c
}

// Unsubscribe removes a client and closes its channel (idempotent).
func (h *Hub) Unsubscribe(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(c)
}

func (h *Hub) removeLocked(c *Client) {
	if set := h.clients[c.vehicleID]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(h.clients, c.vehicleID)
		}
	}
	c.once.Do(func() { close(c.ch) })
}

// Publish delivers msg to every subscriber of msg.VehicleID. A subscriber whose
// buffer is full is disconnected rather than silently missing state; the client
// reconnects and receives a fresh snapshot.
func (h *Hub) Publish(msg Message) {
	if msg.At.IsZero() {
		msg.At = h.now().UTC()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients[msg.VehicleID] {
		select {
		case c.ch <- msg:
		default:
			h.removeLocked(c)
		}
	}
}

// MarkViewed records that someone read the vehicle's live state over REST.
func (h *Hub) MarkViewed(vehicleID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.viewed[vehicleID] = h.now()
}

// Watching reports whether anyone is connected to, or recently viewed, the vehicle.
func (h *Hub) Watching(vehicleID string, within time.Duration) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.clients[vehicleID]) > 0 {
		return true
	}
	t, ok := h.viewed[vehicleID]
	return ok && h.now().Sub(t) < within
}

// Subscribers returns the number of live subscribers for a vehicle.
func (h *Hub) Subscribers(vehicleID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[vehicleID])
}
