// Package transport defines the TeslaTransport abstraction (ADR-003) so the
// backend can swap Fleet (production), Owner (dev/bootstrap) and the simulator
// without touching ingestion, analytics or the API.
package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

// Errors every transport maps its failures onto.
var (
	// ErrVehicleUnavailable: the car is asleep or offline. Never retried by waking
	// implicitly — waking costs money and battery (ADR-013).
	ErrVehicleUnavailable = errors.New("vehicle unavailable (asleep or offline)")
	// ErrReauthRequired: Tesla tokens are no longer valid; the user must sign in again.
	ErrReauthRequired = errors.New("tesla authorization expired; sign in again")
	// ErrUnauthorized: the access token was rejected; callers may refresh once and retry.
	ErrUnauthorized = errors.New("tesla rejected the access token")
	// ErrNotFound: unknown vehicle for this account.
	ErrNotFound = errors.New("vehicle not found")
	// ErrUnsupported: the transport cannot perform the operation.
	ErrUnsupported = errors.New("operation not supported by this transport")
)

// RateLimitError is returned on HTTP 429.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("tesla rate limit; retry after %s", e.RetryAfter)
}

// CommandError is a command the vehicle refused (result=false).
type CommandError struct{ Reason string }

func (e *CommandError) Error() string { return "vehicle refused command: " + e.Reason }

// APIError is any other upstream failure.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("tesla api %d: %s", e.Status, e.Message) }

// Vehicle is a car as listed by Tesla.
type Vehicle struct {
	TeslaID     string
	VIN         string
	DisplayName string
	State       string // online | asleep | offline
	Model       string
	Trim        string
	Color       string
}

// Snapshot is one vehicle_data read: live state plus static config.
type Snapshot struct {
	State       *models.LiveState
	DisplayName string
	Model       string
	Trim        string
	Color       string
}

// Command is a raw Tesla command (name + JSON body).
type Command struct {
	Name string
	Body map[string]any
}

// PollPolicy tunes how often ingestion reads a vehicle in each condition. Fleet
// API calls are billed and rate-limited, so the defaults are conservative and
// let the car sleep (ADR-013).
type PollPolicy struct {
	Driving           time.Duration
	Charging          time.Duration
	OnlineActive      time.Duration // climate on, user present, recently commanded or watched
	OnlineIdle        time.Duration
	AsleepCheck       time.Duration // state-only checks (never wake)
	Watching          time.Duration // someone has the app open on this car
	IdleBeforeSuspend time.Duration // parked & idle this long → stop reading vehicle_data
	Suspend           time.Duration // ...for this long, so the car can fall asleep
	AfterCommand      []time.Duration
}

// Transport is the pluggable interface for talking to Tesla.
type Transport interface {
	Name() string
	// ListVehicles lists the account's vehicles without waking them.
	ListVehicles(ctx context.Context, userID string) ([]Vehicle, error)
	// VehicleState returns online|asleep|offline without waking the car.
	VehicleState(ctx context.Context, userID, vin string) (string, error)
	// VehicleData reads the full live snapshot. Returns ErrVehicleUnavailable when asleep.
	VehicleData(ctx context.Context, userID, vin string) (*Snapshot, error)
	// Wake requests the vehicle to wake (returns immediately; poll VehicleState).
	Wake(ctx context.Context, userID, vin string) error
	// Command executes one raw command on an awake vehicle.
	Command(ctx context.Context, userID, vin string, cmd Command) error
	// Policy returns the polling cadence appropriate for this transport.
	Policy() PollPolicy
}

// HistorySeeder is implemented by transports that can synthesize history (the
// simulator) so dashboards have data on first launch.
type HistorySeeder interface {
	SeedHistory(ctx context.Context, userID, vin string, days int) ([]models.LiveState, error)
}

// Registry resolves a transport by the name stored with a user's credentials.
type Registry map[string]Transport

// Get returns the transport or an error.
func (r Registry) Get(name string) (Transport, error) {
	if t, ok := r[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("transport %q is not enabled on this server", name)
}

// DefaultPolicy is the cost-aware cadence for real Tesla transports.
func DefaultPolicy() PollPolicy {
	return PollPolicy{
		Driving:           30 * time.Second,
		Charging:          60 * time.Second,
		OnlineActive:      60 * time.Second,
		OnlineIdle:        3 * time.Minute,
		AsleepCheck:       5 * time.Minute,
		Watching:          20 * time.Second,
		IdleBeforeSuspend: 12 * time.Minute,
		Suspend:           25 * time.Minute,
		AfterCommand:      []time.Duration{2 * time.Second, 6 * time.Second, 15 * time.Second},
	}
}
