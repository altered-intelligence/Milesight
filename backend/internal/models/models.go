// Package models defines the public API contract shared by every layer of the
// backend and mirrored by the clients (docs/api.md).
//
// Conventions (ADR-009):
//   - JSON keys are snake_case; timestamps are RFC 3339 in UTC.
//   - Units are SI everywhere: km, km/h, °C, kWh, kW, Wh/km. Clients localize.
//   - Optional values are pointers (omitted when unknown) so "0" never means "missing".
package models

import "time"

// APIError is the structured error envelope returned by all endpoints.
type APIError struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries a stable machine code plus a human message.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// ---------------------------------------------------------------- users & auth

// User is the authenticated Milesight account.
type User struct {
	ID          string      `json:"id"`
	Email       string      `json:"email"`
	Plan        string      `json:"plan"`
	Transport   string      `json:"transport,omitempty"` // how this account reaches Tesla: fleet | owner | sim
	CreatedAt   time.Time   `json:"created_at"`
	Preferences Preferences `json:"preferences"`
}

// Preferences are per-user display and cost settings.
type Preferences struct {
	DistanceUnit         string  `json:"distance_unit"`    // km | mi
	TemperatureUnit      string  `json:"temperature_unit"` // c | f
	Currency             string  `json:"currency"`         // ISO 4217
	EnergyRatePerKWh     float64 `json:"energy_rate_per_kwh"`
	FastChargeRatePerKWh float64 `json:"fast_charge_rate_per_kwh"`
	TimeZone             string  `json:"time_zone"` // IANA, used for daily/weekly/monthly buckets
}

// DefaultPreferences are applied to new accounts.
func DefaultPreferences() Preferences {
	return Preferences{
		DistanceUnit: "mi", TemperatureUnit: "f", Currency: "USD",
		EnergyRatePerKWh: 0.15, FastChargeRatePerKWh: 0.45, TimeZone: "UTC",
	}
}

// Session is returned by every sign-in/refresh endpoint.
type Session struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         User      `json:"user"`
}

// ServerConfig is the public, unauthenticated description of this deployment.
type ServerConfig struct {
	Version         string   `json:"version"`
	Transport       string   `json:"transport"`
	DevLogin        bool     `json:"dev_login"`
	TeslaOAuth      bool     `json:"tesla_oauth"`
	OwnerTokenLogin bool     `json:"owner_token_login"`
	Regions         []string `json:"regions"`
}

// ---------------------------------------------------------------- vehicles

// Vehicle is a car the user can access, with its latest known state.
type Vehicle struct {
	ID          string     `json:"id"`
	VIN         string     `json:"vin"`
	DisplayName string     `json:"display_name"`
	Model       string     `json:"model,omitempty"` // "Model 3", "Model Y", ...
	Trim        string     `json:"trim,omitempty"`
	Color       string     `json:"color,omitempty"`
	Region      string     `json:"region"`
	Role        string     `json:"role"`        // owner | viewer
	State       string     `json:"state"`       // online | asleep | offline | unknown
	SyncStatus  string     `json:"sync_status"` // streaming | polling | suspended | idle | error
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	Latest      *LiveState `json:"latest,omitempty"`
}

// Vehicle states.
const (
	StateOnline  = "online"
	StateAsleep  = "asleep"
	StateOffline = "offline"
	StateUnknown = "unknown"
)

// Charge states (normalized from Tesla's charging_state).
const (
	ChargeDisconnected = "disconnected"
	ChargeNoPower      = "no_power"
	ChargeStarting     = "starting"
	ChargeCharging     = "charging"
	ChargeStopped      = "stopped"
	ChargeComplete     = "complete"
)

// LiveState is the aggregate snapshot from GET /v1/vehicles/{id}/live and the
// payload of WebSocket "state" messages.
type LiveState struct {
	VehicleID  string    `json:"vehicle_id"`
	State      string    `json:"state"`
	Battery    Battery   `json:"battery"`
	Charging   *Charging `json:"charging,omitempty"` // present while plugged in
	Climate    *Climate  `json:"climate,omitempty"`
	Drive      *Drive    `json:"drive,omitempty"`
	Location   *Location `json:"location,omitempty"`
	Closures   *Closures `json:"closures,omitempty"`
	Software   *Software `json:"software,omitempty"`
	OdometerKm float64   `json:"odometer_km"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Battery is the high-voltage pack status.
type Battery struct {
	Percent        int     `json:"percent"`
	UsablePercent  int     `json:"usable_percent"`
	RangeKm        float64 `json:"range_km"` // rated range
	IdealRangeKm   float64 `json:"ideal_range_km,omitempty"`
	EstRangeKm     float64 `json:"est_range_km,omitempty"`
	ChargeLimitPct int     `json:"charge_limit_pct"`
	ChargeState    string  `json:"charge_state"`
	PluggedIn      bool    `json:"plugged_in"`
	ChargePortOpen bool    `json:"charge_port_open"`
}

// Charging details while plugged in.
type Charging struct {
	PowerKW         float64 `json:"power_kw"`
	Amps            int     `json:"amps"`
	AmpsRequest     int     `json:"amps_request"`
	AmpsMax         int     `json:"amps_max"`
	Voltage         int     `json:"voltage"`
	Phases          int     `json:"phases,omitempty"`
	EnergyAddedKWh  float64 `json:"energy_added_kwh"`
	RangeAddedKm    float64 `json:"range_added_km"`
	RateKph         float64 `json:"rate_kph"` // km of range added per hour
	MinutesToLimit  int     `json:"minutes_to_limit"`
	FastCharger     bool    `json:"fast_charger"`
	FastChargerType string  `json:"fast_charger_type,omitempty"`
}

// Climate is HVAC state.
type Climate struct {
	On              bool     `json:"on"`
	InsideTempC     *float64 `json:"inside_temp_c,omitempty"`
	OutsideTempC    *float64 `json:"outside_temp_c,omitempty"`
	DriverSetC      float64  `json:"driver_set_c"`
	PassengerSetC   float64  `json:"passenger_set_c"`
	Preconditioning bool     `json:"preconditioning"`
	DefrostOn       bool     `json:"defrost_on"`
	FanStatus       int      `json:"fan_status"`
}

// Drive is motion state.
type Drive struct {
	ShiftState string  `json:"shift_state"` // P | R | N | D | "" (unknown/parked)
	SpeedKph   float64 `json:"speed_kph"`
	PowerKW    float64 `json:"power_kw"` // >0 consuming, <0 regen
}

// Location is a GPS fix plus the matching user place, if any.
type Location struct {
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
	HeadingDeg int     `json:"heading_deg"`
	Place      string  `json:"place,omitempty"`
}

// Closures are locks, doors, windows and modes.
type Closures struct {
	Locked      bool `json:"locked"`
	DoorsOpen   bool `json:"doors_open"`
	WindowsOpen bool `json:"windows_open"`
	FrunkOpen   bool `json:"frunk_open"`
	TrunkOpen   bool `json:"trunk_open"`
	SentryMode  bool `json:"sentry_mode"`
	UserPresent bool `json:"user_present"`
}

// Software is firmware state.
type Software struct {
	Version         string `json:"version"`
	UpdateAvailable bool   `json:"update_available"`
	UpdateVersion   string `json:"update_version,omitempty"`
	UpdateStatus    string `json:"update_status,omitempty"`
}

// IsDriving reports whether the snapshot is in motion / in gear.
func (s *LiveState) IsDriving() bool {
	if s == nil || s.State != StateOnline || s.Drive == nil {
		return false
	}
	switch s.Drive.ShiftState {
	case "D", "R", "N":
		return true
	}
	return s.Drive.SpeedKph > 0
}

// IsCharging reports whether energy is flowing into the pack.
func (s *LiveState) IsCharging() bool {
	if s == nil || s.State != StateOnline {
		return false
	}
	return s.Battery.ChargeState == ChargeCharging || s.Battery.ChargeState == ChargeStarting
}

// ---------------------------------------------------------------- commands

// Command names accepted by POST /v1/vehicles/{id}/commands/{name}.
const (
	CmdClimate         = "climate"
	CmdLock            = "lock"
	CmdUnlock          = "unlock"
	CmdFrunk           = "frunk"
	CmdTrunk           = "trunk"
	CmdChargePort      = "charge_port"
	CmdChargePortClose = "charge_port_close"
	CmdCharging        = "charging"
	CmdHonk            = "honk"
	CmdFlashLights     = "flash_lights"
	CmdSentry          = "sentry"
	CmdWindows         = "windows"
)

// CommandParams is the union of all command bodies; each command validates the
// fields it uses.
type CommandParams struct {
	On       *bool    `json:"on,omitempty"`        // climate, sentry
	TempC    *float64 `json:"temp_c,omitempty"`    // climate
	Action   string   `json:"action,omitempty"`    // charging: start|stop|set_amps|set_limit; windows: vent|close
	Amps     *int     `json:"amps,omitempty"`      // charging set_amps
	LimitPct *int     `json:"limit_pct,omitempty"` // charging set_limit
}

// CommandResult is returned by every command.
type CommandResult struct {
	RequestID string    `json:"request_id"`
	Command   string    `json:"command"`
	Status    string    `json:"status"` // ok | failed
	Reason    string    `json:"reason,omitempty"`
	WokeUp    bool      `json:"woke_up"`
	At        time.Time `json:"at"`
}

// ---------------------------------------------------------------- telemetry & analytics

// TelemetryPoint is one time-series row (raw or bucket-averaged).
type TelemetryPoint struct {
	Time                 time.Time `json:"time"`
	State                string    `json:"state,omitempty"`
	BatteryPct           *float64  `json:"battery_pct,omitempty"`
	UsableBatteryPct     *float64  `json:"usable_battery_pct,omitempty"`
	RatedRangeKm         *float64  `json:"rated_range_km,omitempty"`
	IdealRangeKm         *float64  `json:"ideal_range_km,omitempty"`
	EstRangeKm           *float64  `json:"est_range_km,omitempty"`
	Lat                  *float64  `json:"lat,omitempty"`
	Lon                  *float64  `json:"lon,omitempty"`
	HeadingDeg           *float64  `json:"heading_deg,omitempty"`
	SpeedKph             *float64  `json:"speed_kph,omitempty"`
	PowerKW              *float64  `json:"power_kw,omitempty"`
	OdometerKm           *float64  `json:"odometer_km,omitempty"`
	ShiftState           string    `json:"shift_state,omitempty"`
	InsideTempC          *float64  `json:"inside_temp_c,omitempty"`
	OutsideTempC         *float64  `json:"outside_temp_c,omitempty"`
	ClimateOn            *bool     `json:"climate_on,omitempty"`
	ChargeState          string    `json:"charge_state,omitempty"`
	ChargerPowerKW       *float64  `json:"charger_power_kw,omitempty"`
	ChargerVoltage       *float64  `json:"charger_voltage,omitempty"`
	ChargerAmps          *float64  `json:"charger_amps,omitempty"`
	ChargeEnergyAddedKWh *float64  `json:"charge_energy_added_kwh,omitempty"`
	ChargeLimitPct       *float64  `json:"charge_limit_pct,omitempty"`
	Locked               *bool     `json:"locked,omitempty"`
	SentryMode           *bool     `json:"sentry_mode,omitempty"`
}

// GeoPoint is a bare coordinate.
type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Trip is one automatically detected drive.
type Trip struct {
	ID              string     `json:"id"`
	VehicleID       string     `json:"vehicle_id"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"` // nil while driving
	StartOdometerKm float64    `json:"start_odometer_km"`
	EndOdometerKm   float64    `json:"end_odometer_km"`
	DistanceKm      float64    `json:"distance_km"`
	DurationS       int        `json:"duration_s"`
	AvgSpeedKph     float64    `json:"avg_speed_kph"`
	MaxSpeedKph     float64    `json:"max_speed_kph"`
	StartBatteryPct int        `json:"start_battery_pct"`
	EndBatteryPct   int        `json:"end_battery_pct"`
	StartRangeKm    float64    `json:"start_range_km"`
	EndRangeKm      float64    `json:"end_range_km"`
	EnergyUsedKWh   float64    `json:"energy_used_kwh"`
	EfficiencyWhKm  *float64   `json:"efficiency_wh_km,omitempty"`
	StartLocation   *GeoPoint  `json:"start_location,omitempty"`
	EndLocation     *GeoPoint  `json:"end_location,omitempty"`
	StartPlace      string     `json:"start_place,omitempty"`
	EndPlace        string     `json:"end_place,omitempty"`
	OutsideTempC    *float64   `json:"outside_temp_c,omitempty"`
}

// RoutePoint is one sample along a trip.
type RoutePoint struct {
	Time       time.Time `json:"time"`
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
	SpeedKph   float64   `json:"speed_kph"`
	PowerKW    float64   `json:"power_kw"`
	BatteryPct float64   `json:"battery_pct"`
}

// TripDetail is a trip plus its route.
type TripDetail struct {
	Trip
	Route []RoutePoint `json:"route"`
}

// ChargeSession is one detected charging session.
type ChargeSession struct {
	ID              string     `json:"id"`
	VehicleID       string     `json:"vehicle_id"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"` // nil while charging
	DurationS       int        `json:"duration_s"`
	StartBatteryPct int        `json:"start_battery_pct"`
	EndBatteryPct   int        `json:"end_battery_pct"`
	StartRangeKm    float64    `json:"start_range_km"`
	EndRangeKm      float64    `json:"end_range_km"`
	EnergyAddedKWh  float64    `json:"energy_added_kwh"`
	MaxPowerKW      float64    `json:"max_power_kw"`
	AvgPowerKW      float64    `json:"avg_power_kw"`
	FastCharger     bool       `json:"fast_charger"`
	ChargerType     string     `json:"charger_type,omitempty"`
	Location        *GeoPoint  `json:"location,omitempty"`
	Place           string     `json:"place,omitempty"`
	Cost            *float64   `json:"cost,omitempty"`
	Currency        string     `json:"currency,omitempty"`
	EndReason       string     `json:"end_reason,omitempty"` // complete | stopped | unplugged | asleep
}

// ChargePoint is one sample on a charging curve.
type ChargePoint struct {
	Time           time.Time `json:"time"`
	BatteryPct     float64   `json:"battery_pct"`
	PowerKW        float64   `json:"power_kw"`
	Voltage        float64   `json:"voltage"`
	Amps           float64   `json:"amps"`
	EnergyAddedKWh float64   `json:"energy_added_kwh"`
}

// ChargeDetail is a session plus its charging curve.
type ChargeDetail struct {
	ChargeSession
	Curve []ChargePoint `json:"curve"`
}

// StatsBucket is one day/week/month rollup.
type StatsBucket struct {
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	DistanceKm     float64   `json:"distance_km"`
	DriveTimeS     int       `json:"drive_time_s"`
	Trips          int       `json:"trips"`
	EnergyUsedKWh  float64   `json:"energy_used_kwh"`
	EfficiencyWhKm *float64  `json:"efficiency_wh_km,omitempty"`
	EnergyAddedKWh float64   `json:"energy_added_kwh"`
	Charges        int       `json:"charges"`
	ChargingCost   float64   `json:"charging_cost"`
	DrainKm        float64   `json:"drain_km"`
	DrainKWh       float64   `json:"drain_kwh"`
	IdleHours      float64   `json:"idle_hours"`
}

// StatsResponse is returned by GET /v1/vehicles/{id}/stats/{period}.
type StatsResponse struct {
	Period   string        `json:"period"` // daily | weekly | monthly
	TimeZone string        `json:"time_zone"`
	Currency string        `json:"currency"`
	From     time.Time     `json:"from"`
	To       time.Time     `json:"to"`
	Buckets  []StatsBucket `json:"buckets"`
	Totals   StatsBucket   `json:"totals"`
}

// BatteryHealth is the estimated pack capacity and degradation.
type BatteryHealth struct {
	NominalCapacityKWh   float64              `json:"nominal_capacity_kwh"`
	EstimatedCapacityKWh *float64             `json:"estimated_capacity_kwh,omitempty"`
	NominalFullRangeKm   float64              `json:"nominal_full_range_km"`
	FullRangeKm          *float64             `json:"full_range_km,omitempty"`
	DegradationPct       *float64             `json:"degradation_pct,omitempty"`
	Samples              int                  `json:"samples"`
	History              []BatteryHealthPoint `json:"history"`
}

// BatteryHealthPoint is one monthly estimate.
type BatteryHealthPoint struct {
	Month       time.Time `json:"month"`
	CapacityKWh *float64  `json:"capacity_kwh,omitempty"`
	FullRangeKm *float64  `json:"full_range_km,omitempty"`
	Samples     int       `json:"samples"`
}

// DrainSegment is one parked interval used for vampire-drain tracking.
type DrainSegment struct {
	VehicleID   string    `json:"vehicle_id"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	RangeLostKm float64   `json:"range_lost_km"`
	EnergyKWh   float64   `json:"energy_kwh"`
}

// ---------------------------------------------------------------- places, rules, events, push

// Geofence is a user place.
type Geofence struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Lat              float64  `json:"lat"`
	Lon              float64  `json:"lon"`
	RadiusM          float64  `json:"radius_m"`
	EnergyRatePerKWh *float64 `json:"energy_rate_per_kwh,omitempty"`
}

// Rule toggles one notification type for a user.
type Rule struct {
	Type    string             `json:"type"`
	Enabled bool               `json:"enabled"`
	Params  map[string]float64 `json:"params,omitempty"`
}

// Event (notification) types.
const (
	EventChargeComplete  = "charge_complete"
	EventChargingStarted = "charging_started"
	EventChargingStopped = "charging_stopped"
	EventLowBattery      = "low_battery"
	EventLeftUnlocked    = "left_unlocked"
	EventLeftOpen        = "left_open"
	EventSoftwareUpdate  = "software_update"
	EventGeofenceArrive  = "geofence_arrive"
	EventGeofenceDepart  = "geofence_depart"
	EventTripComplete    = "trip_complete"
)

// Event is a notification-log entry.
type Event struct {
	ID          int64          `json:"id"`
	VehicleID   string         `json:"vehicle_id"`
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	OccurredAt  time.Time      `json:"occurred_at"`
	Payload     map[string]any `json:"payload,omitempty"`
	DeliveredAt *time.Time     `json:"delivered_at,omitempty"`
}

// PushDevice is a registered APNs (or later FCM) token.
type PushDevice struct {
	ID          string    `json:"id"`
	Token       string    `json:"token"`
	Platform    string    `json:"platform"`    // ios
	Environment string    `json:"environment"` // sandbox | production
	CreatedAt   time.Time `json:"created_at"`
}

// Page is the cursor envelope for paginated lists.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
