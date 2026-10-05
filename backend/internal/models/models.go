// Package models defines the domain types shared by the API and transports,
// mirroring docs/api.md (contract-first).
package models

import "time"

// APIError is the structured error envelope returned by all endpoints.
type APIError struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// Vehicle summary as returned by GET /v1/vehicles.
type Vehicle struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	VIN         string    `json:"vin"`
	SyncStatus  string    `json:"sync_status,omitempty"`
	LastSeen    time.Time `json:"last_seen"`
	State       string    `json:"state,omitempty"` // online/asleep/offline
}

// LiveState is the aggregate snapshot from GET /v1/vehicles/{id}/live.
type LiveState struct {
	VehicleID string     `json:"vehicle_id"`
	Battery   Battery    `json:"battery"`
	Location  *Location  `json:"location,omitempty"`
	Climate   *Climate   `json:"climate,omitempty"`
	Locks     *Locks     `json:"locks,omitempty"`
	Software  string     `json:"software,omitempty"`
	OdometerM float64    `json:"odometer_m,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type Battery struct {
	Percent       int      `json:"percent"`
	RangeKm       float64  `json:"range_km"`
	ChargeState   string   `json:"charge_state"` // charging/disconnected/complete
	ChargePort    *string  `json:"charge_port,omitempty"`
	ChargingAmps  *int     `json:"charging_amps,omitempty"`
	ChargeLimitPct int     `json:"charge_limit_pct,omitempty"`
}

type Location struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Heading int     `json:"heading,omitempty"`
}

type Climate struct {
	Enabled   bool     `json:"enabled"`
	TempC     *float64 `json:"temp_c,omitempty"`
	DriverTempC *float64 `json:"driver_temp_c,omitempty"`
}

type Locks struct {
	Locked  bool `json:"locked"`
	Doors   bool `json:"doors"`
	Windows bool `json:"windows"`
	Frunk   bool `json:"frunk"`
	Trunk   bool `json:"trunk"`
}

// CommandRequest/Result for idempotent, rate-limited commands.
type CommandRequest struct {
	VehicleID string         `json:"-"`
	Name      string         `json:"name"`
	Params    map[string]any `json:"params,omitempty"`
}

type CommandResult struct {
	RequestID    string    `json:"request_id"`
	Status       string    `json:"status"` // accepted/queued/failed
	VehicleDelta *LiveState `json:"vehicle_state_delta,omitempty"`
}

// Geofence is a user place.
type Geofence struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	RadiusM  float64 `json:"radius_m"`
}

// TelemetryPoint is one row in a telemetry series.
type TelemetryPoint struct {
	Ts     time.Time              `json:"ts"`
	Fields map[string]jsonValue   `json:"fields"`
}

type jsonValue = any

// Trip and ChargeSession summaries.
type Trip struct {
	ID          string    `json:"id"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	DistanceKm  float64   `json:"distance_km"`
	StartBattery int      `json:"start_battery"`
	EndBattery   int      `json:"end_battery"`
	EfficiencyWhKm *float64 `json:"efficiency_wh_km,omitempty"`
}

type ChargeSession struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	StartBattery int     `json:"start_battery"`
	EndBattery   int     `json:"end_battery"`
	EnergyKWh float64   `json:"energy_kwh"`
}
