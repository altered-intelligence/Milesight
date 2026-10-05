// Package transport defines the TeslaTransport abstraction (ADR-003) so the
// backend can swap Fleet (production) and Owner (dev/bootstrap) transports.
package transport

import (
	"context"

	"github.com/altered-intelligence/milesight/internal/models"
)

// Transport is the pluggable interface for talking to Tesla.
type Transport interface {
	// ListVehicles returns vehicles accessible to the given user.
	ListVehicles(ctx context.Context, userID string) ([]models.Vehicle, error)
	// LiveState returns the current aggregate live state for a vehicle.
	LiveState(ctx context.Context, userID, vehicleID string) (*models.LiveState, error)
	// Wake wakes a sleeping vehicle on demand.
	Wake(ctx context.Context, userID, vehicleID string) error
	// Command executes an idempotent vehicle command.
	Command(ctx context.Context, userID string, req models.CommandRequest) (*models.CommandResult, error)
	// EventStream pushes live deltas for a vehicle to the given sink.
	EventStream(ctx context.Context, userID, vehicleID string, sink chan<- models.LiveState) error
}

// Factory builds a Transport from a name.
type Factory func(ctx context.Context, cfg any) (Transport, error)
