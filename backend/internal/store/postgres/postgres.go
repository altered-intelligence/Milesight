// Package postgres is a stub store. Concrete PGX/Timescale wiring comes next.
package postgres

import (
	"context"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
)

// Store is a placeholder implementing store.Store.
type Store struct{}

func New(databaseURL string) (*Store, error) { return &Store{}, nil }

func (s *Store) ListVehicles(_ context.Context, _ string) ([]models.Vehicle, error) {
	return []models.Vehicle{}, nil
}

func (s *Store) SaveLiveState(_ context.Context, _ *models.LiveState) error { return nil }
func (s *Store) SaveCommandResult(_ context.Context, _ *models.CommandResult) error { return nil }
func (s *Store) UpsertGeofence(_ context.Context, _ string, _ models.Geofence) error { return nil }

func (s *Store) WritePoint(_ context.Context, _ string, _ models.TelemetryPoint) error { return nil }
func (s *Store) QuerySeries(_ context.Context, _ string, _ time.Time, _ time.Time,
	_ []string, _ string) ([]models.TelemetryPoint, error) {
	return []models.TelemetryPoint{}, nil
}

var _ store.Store = (*Store)(nil)
