// Package store defines persistence interfaces for entities and telemetry.
package store

import (
	"context"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

// Store is the aggregate persistence interface used by the API layer.
type Store interface {
	EntityStore
	TelemetryStore
}

// EntityStore handles relational entities (PostgreSQL).
type EntityStore interface {
	ListVehicles(ctx context.Context, userID string) ([]models.Vehicle, error)
	SaveLiveState(ctx context.Context, st *models.LiveState) error
	SaveCommandResult(ctx context.Context, res *models.CommandResult) error
	UpsertGeofence(ctx context.Context, userID string, g models.Geofence) error
}

// TelemetryStore handles time-series (TimescaleDB).
type TelemetryStore interface {
	WritePoint(ctx context.Context, vehicleID string, p models.TelemetryPoint) error
	QuerySeries(ctx context.Context, vehicleID string, from, to time.Time,
		fields []string, res string) ([]models.TelemetryPoint, error)
}
