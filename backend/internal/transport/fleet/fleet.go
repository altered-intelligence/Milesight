// Package fleet is the official Tesla Fleet API transport used in production
// (ADR-002). It is a stub pending developer access + concrete OAuth.
package fleet

import (
	"context"
	"fmt"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// Transport implements transport.Transport against the official Tesla Fleet API.
type Transport struct{}

func New(cfg any) (transport.Transport, error) { return &Transport{}, nil }

func (t *Transport) ListVehicles(_ context.Context, userID string) ([]models.Vehicle, error) {
	return nil, fmt.Errorf("fleet ListVehicles: not implemented (userID=%s)", userID)
}

func (t *Transport) LiveState(_ context.Context, _ string, vehicleID string) (*models.LiveState, error) {
	return nil, fmt.Errorf("fleet LiveState: not implemented (vehicleID=%s)", vehicleID)
}

func (t *Transport) Wake(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("fleet Wake: not implemented")
}

func (t *Transport) Command(_ context.Context, _ string, req models.CommandRequest) (*models.CommandResult, error) {
	return nil, fmt.Errorf("fleet Command: not implemented (name=%s)", req.Name)
}

func (t *Transport) EventStream(_ context.Context, _ string, _ string, _ chan<- models.LiveState) error {
	return fmt.Errorf("fleet EventStream: not implemented")
}
