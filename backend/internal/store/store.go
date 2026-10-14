// Package store defines the persistence contract. Two implementations exist:
// postgres (production; Timescale-aware) and memory (zero-infra dev + tests).
package store

import (
	"context"
	"errors"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

// Sentinel errors returned by every implementation.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
)

// Credentials are a user's Tesla tokens, encrypted at rest by the caller.
type Credentials struct {
	UserID          string
	Transport       string // fleet | owner | sim
	AccessTokenEnc  []byte
	RefreshTokenEnc []byte
	Scopes          []string
	Region          string
	ExpiresAt       time.Time
	UpdatedAt       time.Time
}

// RefreshToken is a Milesight session refresh token (only its hash is stored).
type RefreshToken struct {
	ID        string
	UserID    string
	FamilyID  string
	Hash      []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// AuthState is a pending OAuth authorization (one-shot, short-lived).
type AuthState struct {
	State        string
	Transport    string
	Region       string
	RedirectURI  string
	CodeVerifier string
	AppRedirect  string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// VehicleRecord is the stored vehicle row.
type VehicleRecord struct {
	ID          string
	VIN         string
	TeslaID     string
	DisplayName string
	Model       string
	Trim        string
	Color       string
	Region      string
	State       string
	SyncStatus  string
	LastSeen    *time.Time
	Latest      *models.LiveState
	CreatedAt   time.Time
}

// ToModel converts the record to the API shape for a given role.
func (v *VehicleRecord) ToModel(role string) models.Vehicle {
	return models.Vehicle{
		ID: v.ID, VIN: v.VIN, DisplayName: v.DisplayName, Model: v.Model, Trim: v.Trim,
		Color: v.Color, Region: v.Region, Role: role, State: v.State, SyncStatus: v.SyncStatus,
		LastSeen: v.LastSeen, Latest: v.Latest,
	}
}

// VehicleUser is one membership row.
type VehicleUser struct {
	UserID string
	Role   string
}

// PollTarget is a vehicle plus the account whose credentials ingestion uses.
type PollTarget struct {
	Vehicle   VehicleRecord
	UserID    string
	Transport string
}

// ListOptions bounds and paginates time-ordered lists (newest first).
type ListOptions struct {
	From   time.Time // inclusive; zero = unbounded
	To     time.Time // exclusive; zero = unbounded
	Before *time.Time
	Limit  int
}

// IdempotencyRecord caches the response to a mutating request.
type IdempotencyRecord struct {
	UserID      string
	Key         string
	RequestHash string
	StatusCode  int
	Body        []byte
	CreatedAt   time.Time
}

// CommandLog is the audit trail for remote commands.
type CommandLog struct {
	ID        string
	VehicleID string
	UserID    string
	Name      string
	Params    models.CommandParams
	Status    string
	Reason    string
	CreatedAt time.Time
}

// Store is the full persistence contract.
type Store interface {
	Migrate(ctx context.Context) error
	Close()

	// Users
	UpsertUser(ctx context.Context, externalID, email, transport string) (*models.User, error)
	GetUser(ctx context.Context, id string) (*models.User, error)
	UpdatePreferences(ctx context.Context, userID string, p models.Preferences) error
	// DeleteUser removes the account and everything it owns. Vehicles left with
	// no members are deleted together with their telemetry and history.
	DeleteUser(ctx context.Context, userID string) error

	// Tesla credentials
	SaveCredentials(ctx context.Context, c *Credentials) error
	GetCredentials(ctx context.Context, userID string) (*Credentials, error)

	// Session refresh tokens
	InsertRefreshToken(ctx context.Context, t *RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, hash []byte) (*RefreshToken, error)
	// MarkRefreshTokenUsed atomically marks an unused token used; it returns
	// false if the token was already used or revoked (reuse detection).
	MarkRefreshTokenUsed(ctx context.Context, id string, at time.Time) (bool, error)
	RevokeRefreshFamily(ctx context.Context, familyID string, at time.Time) error
	RevokeUserRefreshTokens(ctx context.Context, userID string, at time.Time) error

	// OAuth states
	SaveAuthState(ctx context.Context, s *AuthState) error
	ConsumeAuthState(ctx context.Context, state string, now time.Time) (*AuthState, error)

	// Vehicles
	UpsertVehicle(ctx context.Context, v *VehicleRecord) error // keyed by VIN; sets v.ID
	LinkVehicle(ctx context.Context, userID, vehicleID, role string) error
	ListUserVehicles(ctx context.Context, userID string) ([]models.Vehicle, error)
	GetUserVehicle(ctx context.Context, userID, vehicleID string) (*VehicleRecord, string, error)
	GetVehicle(ctx context.Context, vehicleID string) (*VehicleRecord, error)
	VehicleUsers(ctx context.Context, vehicleID string) ([]VehicleUser, error)
	RenameVehicle(ctx context.Context, vehicleID, name string) error
	UpdateVehicleStatus(ctx context.Context, vehicleID, state, syncStatus string, lastSeen *time.Time, latest *models.LiveState) error
	ListPollTargets(ctx context.Context) ([]PollTarget, error)
	GetProcessorState(ctx context.Context, vehicleID string) ([]byte, error)
	SaveProcessorState(ctx context.Context, vehicleID string, state []byte) error

	// Telemetry
	InsertTelemetry(ctx context.Context, vehicleID string, pts []models.TelemetryPoint) error
	// QueryTelemetry returns points in [from, to) ascending. bucket > 0 averages
	// numeric fields per bucket (non-numeric fields take the last value).
	QueryTelemetry(ctx context.Context, vehicleID string, from, to time.Time, bucket time.Duration, limit int) ([]models.TelemetryPoint, error)

	// Trips, charging sessions, drain
	SaveTrip(ctx context.Context, t *models.Trip) error
	ListTrips(ctx context.Context, vehicleID string, o ListOptions) ([]models.Trip, error)
	GetTrip(ctx context.Context, vehicleID, id string) (*models.Trip, error)
	SaveChargeSession(ctx context.Context, c *models.ChargeSession) error
	ListChargeSessions(ctx context.Context, vehicleID string, o ListOptions) ([]models.ChargeSession, error)
	GetChargeSession(ctx context.Context, vehicleID, id string) (*models.ChargeSession, error)
	InsertDrain(ctx context.Context, d models.DrainSegment) error
	ListDrain(ctx context.Context, vehicleID string, from, to time.Time) ([]models.DrainSegment, error)

	// Events
	InsertEvent(ctx context.Context, e *models.Event) error
	ListEvents(ctx context.Context, userID, vehicleID string, limit int, beforeID int64) ([]models.Event, error)
	MarkEventDelivered(ctx context.Context, id int64, at time.Time) error

	// Notification rules (only explicit settings are stored; defaults live in notify)
	GetRules(ctx context.Context, userID string) ([]models.Rule, error)
	SaveRules(ctx context.Context, userID string, rules []models.Rule) error

	// Geofences
	ListGeofences(ctx context.Context, userID string) ([]models.Geofence, error)
	GetGeofence(ctx context.Context, userID, id string) (*models.Geofence, error)
	CreateGeofence(ctx context.Context, userID string, g *models.Geofence) error
	UpdateGeofence(ctx context.Context, userID string, g *models.Geofence) error
	DeleteGeofence(ctx context.Context, userID, id string) error

	// Push devices
	UpsertPushDevice(ctx context.Context, userID string, d *models.PushDevice) error
	ListPushDevices(ctx context.Context, userID string) ([]models.PushDevice, error)
	DeletePushDevice(ctx context.Context, userID, token string) error
	DeletePushToken(ctx context.Context, token string) error

	// Idempotency + audit
	GetIdempotency(ctx context.Context, userID, key string) (*IdempotencyRecord, error)
	SaveIdempotency(ctx context.Context, r *IdempotencyRecord) error
	InsertCommandLog(ctx context.Context, l *CommandLog) error
}
