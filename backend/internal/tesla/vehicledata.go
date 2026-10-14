package tesla

import (
	"math"
	"strings"
	"time"

	"github.com/altered-intelligence/milesight/internal/geo"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// VehicleDataEndpoints is the `endpoints` query value for vehicle_data.
// location_data is required for GPS on firmware 2023.38+.
const VehicleDataEndpoints = "charge_state;climate_state;closures_state;drive_state;gui_settings;location_data;vehicle_config;vehicle_state"

// VehicleSummary is an entry of GET /api/1/vehicles.
type VehicleSummary struct {
	ID          int64  `json:"id"`
	IDS         string `json:"id_s"`
	VehicleID   int64  `json:"vehicle_id"`
	VIN         string `json:"vin"`
	DisplayName string `json:"display_name"`
	State       string `json:"state"`
}

// VehicleData is the subset of GET /vehicle_data that Milesight consumes.
// Every field is optional: Tesla omits sub-states it cannot read.
type VehicleData struct {
	VehicleSummary
	ChargeState   *ChargeState   `json:"charge_state"`
	ClimateState  *ClimateState  `json:"climate_state"`
	DriveState    *DriveState    `json:"drive_state"`
	VehicleState  *VehicleState  `json:"vehicle_state"`
	VehicleConfig *VehicleConfig `json:"vehicle_config"`
}

// ChargeState — distances in miles, power in kW, energy in kWh.
type ChargeState struct {
	BatteryLevel            *float64 `json:"battery_level"`
	UsableBatteryLevel      *float64 `json:"usable_battery_level"`
	BatteryRange            *float64 `json:"battery_range"`
	IdealBatteryRange       *float64 `json:"ideal_battery_range"`
	EstBatteryRange         *float64 `json:"est_battery_range"`
	ChargeLimitSoc          *float64 `json:"charge_limit_soc"`
	ChargingState           string   `json:"charging_state"`
	ChargePortDoorOpen      *bool    `json:"charge_port_door_open"`
	ChargePortLatch         string   `json:"charge_port_latch"`
	ChargerPower            *float64 `json:"charger_power"`
	ChargerVoltage          *float64 `json:"charger_voltage"`
	ChargerActualCurrent    *float64 `json:"charger_actual_current"`
	ChargeCurrentRequest    *float64 `json:"charge_current_request"`
	ChargeCurrentRequestMax *float64 `json:"charge_current_request_max"`
	ChargerPhases           *float64 `json:"charger_phases"`
	ChargeEnergyAdded       *float64 `json:"charge_energy_added"`
	ChargeMilesAddedRated   *float64 `json:"charge_miles_added_rated"`
	ChargeRate              *float64 `json:"charge_rate"`
	MinutesToFullCharge     *float64 `json:"minutes_to_full_charge"`
	FastChargerPresent      *bool    `json:"fast_charger_present"`
	FastChargerType         string   `json:"fast_charger_type"`
	ConnChargeCable         string   `json:"conn_charge_cable"`
	Timestamp               int64    `json:"timestamp"`
}

// ClimateState — temperatures in °C.
type ClimateState struct {
	InsideTemp           *float64 `json:"inside_temp"`
	OutsideTemp          *float64 `json:"outside_temp"`
	DriverTempSetting    *float64 `json:"driver_temp_setting"`
	PassengerTempSetting *float64 `json:"passenger_temp_setting"`
	IsClimateOn          *bool    `json:"is_climate_on"`
	IsPreconditioning    *bool    `json:"is_preconditioning"`
	IsFrontDefrosterOn   *bool    `json:"is_front_defroster_on"`
	FanStatus            *float64 `json:"fan_status"`
	Timestamp            int64    `json:"timestamp"`
}

// DriveState — speed in mph, power in kW.
type DriveState struct {
	ShiftState *string  `json:"shift_state"`
	Speed      *float64 `json:"speed"`
	Power      *float64 `json:"power"`
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
	Heading    *float64 `json:"heading"`
	Timestamp  int64    `json:"timestamp"`
}

// VehicleState — odometer in miles; door/window fields are 0/1 integers.
type VehicleState struct {
	Locked         *bool    `json:"locked"`
	Odometer       *float64 `json:"odometer"`
	Df             *float64 `json:"df"`
	Dr             *float64 `json:"dr"`
	Pf             *float64 `json:"pf"`
	Pr             *float64 `json:"pr"`
	FdWindow       *float64 `json:"fd_window"`
	FpWindow       *float64 `json:"fp_window"`
	RdWindow       *float64 `json:"rd_window"`
	RpWindow       *float64 `json:"rp_window"`
	Ft             *float64 `json:"ft"`
	Rt             *float64 `json:"rt"`
	SentryMode     *bool    `json:"sentry_mode"`
	IsUserPresent  *bool    `json:"is_user_present"`
	CarVersion     string   `json:"car_version"`
	SoftwareUpdate *struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	} `json:"software_update"`
	Timestamp int64 `json:"timestamp"`
}

// VehicleConfig is static configuration.
type VehicleConfig struct {
	CarType       string `json:"car_type"`
	TrimBadging   string `json:"trim_badging"`
	ExteriorColor string `json:"exterior_color"`
}

// ModelName turns Tesla's car_type into a display name.
func ModelName(carType string) string {
	switch strings.ToLower(carType) {
	case "model3", "highland":
		return "Model 3"
	case "modely", "juniper":
		return "Model Y"
	case "models", "models2", "lychee":
		return "Model S"
	case "modelx", "tamarind":
		return "Model X"
	case "cybertruck":
		return "Cybertruck"
	case "":
		return ""
	default:
		return carType
	}
}

// NormalizeChargeState maps Tesla's charging_state onto Milesight's vocabulary.
func NormalizeChargeState(s string) string {
	switch strings.ToLower(s) {
	case "", "disconnected":
		return models.ChargeDisconnected
	case "charging":
		return models.ChargeCharging
	case "complete":
		return models.ChargeComplete
	case "stopped":
		return models.ChargeStopped
	case "starting":
		return models.ChargeStarting
	case "nopower", "no_power":
		return models.ChargeNoPower
	default:
		return strings.ToLower(s)
	}
}

func val(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func flag(p *bool) bool { return p != nil && *p }

func open(ps ...*float64) bool {
	for _, p := range ps {
		if p != nil && *p != 0 {
			return true
		}
	}
	return false
}

func km(mi *float64) float64 { return geo.Round(geo.MilesToKm(val(mi)), 2) }

// ToSnapshot maps vehicle_data deterministically onto a Snapshot. fetchedAt is
// the server time of the read (the server clock is authoritative).
func ToSnapshot(vd *VehicleData, fetchedAt time.Time) *transport.Snapshot {
	st := &models.LiveState{State: strings.ToLower(vd.State), UpdatedAt: fetchedAt.UTC()}
	if st.State == "" {
		st.State = models.StateOnline
	}
	if cs := vd.ChargeState; cs != nil {
		chargeState := NormalizeChargeState(cs.ChargingState)
		plugged := chargeState != models.ChargeDisconnected ||
			strings.EqualFold(cs.ChargePortLatch, "Engaged") && cs.ConnChargeCable != "" && cs.ConnChargeCable != "<invalid>"
		st.Battery = models.Battery{
			Percent:        int(math.Round(val(cs.BatteryLevel))),
			UsablePercent:  int(math.Round(val(cs.UsableBatteryLevel))),
			RangeKm:        km(cs.BatteryRange),
			IdealRangeKm:   km(cs.IdealBatteryRange),
			EstRangeKm:     km(cs.EstBatteryRange),
			ChargeLimitPct: int(math.Round(val(cs.ChargeLimitSoc))),
			ChargeState:    chargeState,
			PluggedIn:      plugged,
			ChargePortOpen: flag(cs.ChargePortDoorOpen),
		}
		if st.Battery.UsablePercent == 0 {
			st.Battery.UsablePercent = st.Battery.Percent
		}
		if plugged {
			st.Charging = &models.Charging{
				PowerKW:         val(cs.ChargerPower),
				Amps:            int(val(cs.ChargerActualCurrent)),
				AmpsRequest:     int(val(cs.ChargeCurrentRequest)),
				AmpsMax:         int(val(cs.ChargeCurrentRequestMax)),
				Voltage:         int(val(cs.ChargerVoltage)),
				Phases:          int(val(cs.ChargerPhases)),
				EnergyAddedKWh:  val(cs.ChargeEnergyAdded),
				RangeAddedKm:    km(cs.ChargeMilesAddedRated),
				RateKph:         km(cs.ChargeRate),
				MinutesToLimit:  int(val(cs.MinutesToFullCharge)),
				FastCharger:     flag(cs.FastChargerPresent),
				FastChargerType: fastChargerType(cs),
			}
		}
	}
	if cl := vd.ClimateState; cl != nil {
		st.Climate = &models.Climate{
			On: flag(cl.IsClimateOn), InsideTempC: cl.InsideTemp, OutsideTempC: cl.OutsideTemp,
			DriverSetC: val(cl.DriverTempSetting), PassengerSetC: val(cl.PassengerTempSetting),
			Preconditioning: flag(cl.IsPreconditioning), DefrostOn: flag(cl.IsFrontDefrosterOn), FanStatus: int(val(cl.FanStatus)),
		}
	}
	if ds := vd.DriveState; ds != nil {
		shift := ""
		if ds.ShiftState != nil {
			shift = strings.ToUpper(*ds.ShiftState)
		}
		st.Drive = &models.Drive{ShiftState: shift, SpeedKph: geo.Round(geo.MilesToKm(val(ds.Speed)), 1), PowerKW: val(ds.Power)}
		if ds.Latitude != nil && ds.Longitude != nil && !(*ds.Latitude == 0 && *ds.Longitude == 0) {
			st.Location = &models.Location{Lat: *ds.Latitude, Lon: *ds.Longitude, HeadingDeg: int(val(ds.Heading))}
		}
	}
	if vs := vd.VehicleState; vs != nil {
		st.OdometerKm = km(vs.Odometer)
		st.Closures = &models.Closures{
			Locked:      flag(vs.Locked),
			DoorsOpen:   open(vs.Df, vs.Dr, vs.Pf, vs.Pr),
			WindowsOpen: open(vs.FdWindow, vs.FpWindow, vs.RdWindow, vs.RpWindow),
			FrunkOpen:   open(vs.Ft),
			TrunkOpen:   open(vs.Rt),
			SentryMode:  flag(vs.SentryMode),
			UserPresent: flag(vs.IsUserPresent),
		}
		sw := &models.Software{Version: strings.TrimSpace(strings.SplitN(vs.CarVersion, " ", 2)[0])}
		if u := vs.SoftwareUpdate; u != nil {
			sw.UpdateStatus = u.Status
			sw.UpdateVersion = strings.TrimSpace(u.Version)
			sw.UpdateAvailable = u.Status != "" && sw.UpdateVersion != "" && sw.UpdateVersion != sw.Version
		}
		st.Software = sw
	}
	snap := &transport.Snapshot{State: st, DisplayName: vd.DisplayName}
	if vc := vd.VehicleConfig; vc != nil {
		snap.Model, snap.Trim, snap.Color = ModelName(vc.CarType), vc.TrimBadging, vc.ExteriorColor
	}
	return snap
}

func fastChargerType(cs *ChargeState) string {
	if !flag(cs.FastChargerPresent) {
		return ""
	}
	t := cs.FastChargerType
	if t == "" || t == "<invalid>" {
		return "dc"
	}
	return t
}
