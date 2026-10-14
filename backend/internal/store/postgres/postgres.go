// Package postgres implements store.Store on PostgreSQL via pgx, with optional
// TimescaleDB acceleration for telemetry (ADR-005, ADR-012).
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is the PostgreSQL store.
type Store struct {
	pool *pgxpool.Pool
}

var _ store.Store = (*Store)(nil)

// New connects to databaseURL.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	if databaseURL == "" {
		return nil, errors.New("postgres: DATABASE_URL is empty")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Migrate applies embedded migrations exactly once, serialized across instances
// with an advisory lock.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	const lockKey = 7_413_291_001
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("migrate: lock: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("migrate: bootstrap: %w", err)
	}
	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(strings.TrimPrefix(f, "migrations/"), ".sql")
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrationsFS.ReadFile(f)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ---------------------------------------------------------------- users

const userCols = `id::text, email, plan, transport, created_at, preferences`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	var prefs []byte
	if err := row.Scan(&u.ID, &u.Email, &u.Plan, &u.Transport, &u.CreatedAt, &prefs); err != nil {
		return nil, notFound(err)
	}
	u.Preferences = models.DefaultPreferences()
	if len(prefs) > 0 {
		_ = json.Unmarshal(prefs, &u.Preferences) // missing keys keep defaults
	}
	u.CreatedAt = u.CreatedAt.UTC()
	return &u, nil
}

func (s *Store) UpsertUser(ctx context.Context, externalID, email, transport string) (*models.User, error) {
	prefs, _ := json.Marshal(models.DefaultPreferences())
	return scanUser(s.pool.QueryRow(ctx, `
		INSERT INTO users (external_id, email, transport, preferences) VALUES ($1, $2, $3, $4)
		ON CONFLICT (external_id) DO UPDATE
		   SET email = CASE WHEN EXCLUDED.email <> '' THEN EXCLUDED.email ELSE users.email END,
		       transport = EXCLUDED.transport
		RETURNING `+userCols, externalID, email, transport, prefs))
}

func (s *Store) GetUser(ctx context.Context, id string) (*models.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (s *Store) UpdatePreferences(ctx context.Context, userID string, p models.Preferences) error {
	b, _ := json.Marshal(p)
	tag, err := s.pool.Exec(ctx, `UPDATE users SET preferences=$2 WHERE id=$1`, userID, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT vehicle_id::text FROM user_vehicles WHERE user_id=$1`, userID)
		if err != nil {
			return err
		}
		vids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if len(vids) > 0 {
			_, err = tx.Exec(ctx, `DELETE FROM vehicles v WHERE v.id::text = ANY($1)
				AND NOT EXISTS (SELECT 1 FROM user_vehicles uv WHERE uv.vehicle_id = v.id)`, vids)
		}
		return err
	})
}

// ---------------------------------------------------------------- credentials

func (s *Store) SaveCredentials(ctx context.Context, c *store.Credentials) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tesla_credentials (user_id, transport, access_token_enc, refresh_token_enc, scopes, region, expires_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (user_id) DO UPDATE SET transport=EXCLUDED.transport, access_token_enc=EXCLUDED.access_token_enc,
		    refresh_token_enc=EXCLUDED.refresh_token_enc, scopes=EXCLUDED.scopes, region=EXCLUDED.region,
		    expires_at=EXCLUDED.expires_at, updated_at=now()`,
		c.UserID, c.Transport, c.AccessTokenEnc, c.RefreshTokenEnc, nonNil(c.Scopes), c.Region, c.ExpiresAt)
	return err
}

func (s *Store) GetCredentials(ctx context.Context, userID string) (*store.Credentials, error) {
	var c store.Credentials
	err := s.pool.QueryRow(ctx, `SELECT user_id::text, transport, access_token_enc, refresh_token_enc, scopes, region, expires_at, updated_at
		FROM tesla_credentials WHERE user_id=$1`, userID).
		Scan(&c.UserID, &c.Transport, &c.AccessTokenEnc, &c.RefreshTokenEnc, &c.Scopes, &c.Region, &c.ExpiresAt, &c.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------------------------------------------------------------- refresh tokens

func (s *Store) InsertRefreshToken(ctx context.Context, t *store.RefreshToken) error {
	if t.ID == "" {
		t.ID = ids.New()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, t.ID, t.UserID, t.FamilyID, t.Hash, t.CreatedAt, t.ExpiresAt)
	return err
}

func (s *Store) GetRefreshTokenByHash(ctx context.Context, hash []byte) (*store.RefreshToken, error) {
	var t store.RefreshToken
	err := s.pool.QueryRow(ctx, `SELECT id::text, user_id::text, family_id::text, token_hash, created_at, expires_at, used_at, revoked_at
		FROM refresh_tokens WHERE token_hash=$1`, hash).
		Scan(&t.ID, &t.UserID, &t.FamilyID, &t.Hash, &t.CreatedAt, &t.ExpiresAt, &t.UsedAt, &t.RevokedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &t, nil
}

func (s *Store) MarkRefreshTokenUsed(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET used_at=$2 WHERE id=$1 AND used_at IS NULL AND revoked_at IS NULL`, id, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) RevokeRefreshFamily(ctx context.Context, familyID string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=$2 WHERE family_id=$1 AND revoked_at IS NULL`, familyID, at)
	return err
}

func (s *Store) RevokeUserRefreshTokens(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, at)
	return err
}

// ---------------------------------------------------------------- auth states

func (s *Store) SaveAuthState(ctx context.Context, a *store.AuthState) error {
	// Opportunistically prune expired states.
	_, _ = s.pool.Exec(ctx, `DELETE FROM auth_states WHERE expires_at < now() - interval '1 hour'`)
	_, err := s.pool.Exec(ctx, `INSERT INTO auth_states (state, transport, region, redirect_uri, code_verifier, app_redirect, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, a.State, a.Transport, a.Region, a.RedirectURI, a.CodeVerifier, a.AppRedirect, a.CreatedAt, a.ExpiresAt)
	return err
}

func (s *Store) ConsumeAuthState(ctx context.Context, state string, now time.Time) (*store.AuthState, error) {
	var a store.AuthState
	err := s.pool.QueryRow(ctx, `DELETE FROM auth_states WHERE state=$1
		RETURNING state, transport, region, redirect_uri, code_verifier, app_redirect, created_at, expires_at`, state).
		Scan(&a.State, &a.Transport, &a.Region, &a.RedirectURI, &a.CodeVerifier, &a.AppRedirect, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		return nil, notFound(err)
	}
	if now.After(a.ExpiresAt) {
		return nil, store.ErrNotFound
	}
	return &a, nil
}

// ---------------------------------------------------------------- vehicles

const vehicleCols = `v.id::text, v.vin, v.tesla_id, v.display_name, v.model, v.trim, v.color, v.region,
	v.state, v.sync_status, v.last_seen, v.latest, v.created_at`

func scanVehicle(row pgx.Row, extra ...any) (*store.VehicleRecord, error) {
	var v store.VehicleRecord
	var latest []byte
	dst := append([]any{&v.ID, &v.VIN, &v.TeslaID, &v.DisplayName, &v.Model, &v.Trim, &v.Color, &v.Region,
		&v.State, &v.SyncStatus, &v.LastSeen, &latest, &v.CreatedAt}, extra...)
	if err := row.Scan(dst...); err != nil {
		return nil, notFound(err)
	}
	if len(latest) > 0 {
		var ls models.LiveState
		if json.Unmarshal(latest, &ls) == nil {
			v.Latest = &ls
		}
	}
	if v.LastSeen != nil {
		t := v.LastSeen.UTC()
		v.LastSeen = &t
	}
	return &v, nil
}

func (s *Store) UpsertVehicle(ctx context.Context, v *store.VehicleRecord) error {
	if v.Region == "" {
		v.Region = "na"
	}
	return s.pool.QueryRow(ctx, `
		INSERT INTO vehicles (vin, tesla_id, display_name, model, trim, color, region)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (vin) DO UPDATE SET
		    tesla_id = EXCLUDED.tesla_id,
		    display_name = CASE WHEN vehicles.display_name = '' THEN EXCLUDED.display_name ELSE vehicles.display_name END,
		    model = CASE WHEN EXCLUDED.model <> '' THEN EXCLUDED.model ELSE vehicles.model END,
		    trim  = CASE WHEN EXCLUDED.trim  <> '' THEN EXCLUDED.trim  ELSE vehicles.trim  END,
		    color = CASE WHEN EXCLUDED.color <> '' THEN EXCLUDED.color ELSE vehicles.color END,
		    region = EXCLUDED.region
		RETURNING id::text`, v.VIN, v.TeslaID, v.DisplayName, v.Model, v.Trim, v.Color, v.Region).Scan(&v.ID)
}

func (s *Store) LinkVehicle(ctx context.Context, userID, vehicleID, role string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO user_vehicles (user_id, vehicle_id, role) VALUES ($1,$2,$3)
		ON CONFLICT (user_id, vehicle_id) DO UPDATE SET role=EXCLUDED.role`, userID, vehicleID, role)
	return err
}

func (s *Store) ListUserVehicles(ctx context.Context, userID string) ([]models.Vehicle, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+vehicleCols+`, uv.role FROM vehicles v
		JOIN user_vehicles uv ON uv.vehicle_id = v.id WHERE uv.user_id=$1 ORDER BY v.display_name, v.vin`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Vehicle{}
	for rows.Next() {
		var role string
		v, err := scanVehicle(rows, &role)
		if err != nil {
			return nil, err
		}
		out = append(out, v.ToModel(role))
	}
	return out, rows.Err()
}

func (s *Store) GetUserVehicle(ctx context.Context, userID, vehicleID string) (*store.VehicleRecord, string, error) {
	var role string
	v, err := scanVehicle(s.pool.QueryRow(ctx, `SELECT `+vehicleCols+`, uv.role FROM vehicles v
		JOIN user_vehicles uv ON uv.vehicle_id = v.id WHERE uv.user_id=$1 AND v.id=$2`, userID, vehicleID), &role)
	if err != nil {
		return nil, "", err
	}
	return v, role, nil
}

func (s *Store) GetVehicle(ctx context.Context, vehicleID string) (*store.VehicleRecord, error) {
	return scanVehicle(s.pool.QueryRow(ctx, `SELECT `+vehicleCols+` FROM vehicles v WHERE v.id=$1`, vehicleID))
}

func (s *Store) VehicleUsers(ctx context.Context, vehicleID string) ([]store.VehicleUser, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id::text, role FROM user_vehicles WHERE vehicle_id=$1 ORDER BY user_id`, vehicleID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (store.VehicleUser, error) {
		var u store.VehicleUser
		err := r.Scan(&u.UserID, &u.Role)
		return u, err
	})
}

func (s *Store) RenameVehicle(ctx context.Context, vehicleID, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE vehicles SET display_name=$2 WHERE id=$1`, vehicleID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) UpdateVehicleStatus(ctx context.Context, vehicleID, state, syncStatus string, lastSeen *time.Time, latest *models.LiveState) error {
	var latestJSON []byte
	if latest != nil {
		latestJSON, _ = json.Marshal(latest)
	}
	_, err := s.pool.Exec(ctx, `UPDATE vehicles SET
		state = COALESCE(NULLIF($2, ''), state),
		sync_status = COALESCE(NULLIF($3, ''), sync_status),
		last_seen = COALESCE($4, last_seen),
		latest = COALESCE($5::jsonb, latest)
		WHERE id=$1`, vehicleID, state, syncStatus, lastSeen, latestJSON)
	return err
}

func (s *Store) ListPollTargets(ctx context.Context) ([]store.PollTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (v.id) `+vehicleCols+`, uv.user_id::text, c.transport
		FROM vehicles v
		JOIN user_vehicles uv ON uv.vehicle_id = v.id AND uv.role = 'owner'
		JOIN tesla_credentials c ON c.user_id = uv.user_id
		ORDER BY v.id, uv.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.PollTarget
	for rows.Next() {
		var t store.PollTarget
		v, err := scanVehicle(rows, &t.UserID, &t.Transport)
		if err != nil {
			return nil, err
		}
		t.Vehicle = *v
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetProcessorState(ctx context.Context, vehicleID string) ([]byte, error) {
	var b []byte
	if err := s.pool.QueryRow(ctx, `SELECT processor_state FROM vehicles WHERE id=$1`, vehicleID).Scan(&b); err != nil {
		return nil, notFound(err)
	}
	if b == nil {
		return nil, store.ErrNotFound
	}
	return b, nil
}

func (s *Store) SaveProcessorState(ctx context.Context, vehicleID string, state []byte) error {
	_, err := s.pool.Exec(ctx, `UPDATE vehicles SET processor_state=$2::jsonb WHERE id=$1`, vehicleID, state)
	return err
}

// ---------------------------------------------------------------- telemetry

var telemetryCols = []string{"time", "vehicle_id", "state", "battery_pct", "usable_battery_pct", "rated_range_km",
	"ideal_range_km", "est_range_km", "lat", "lon", "heading_deg", "speed_kph", "power_kw", "odometer_km",
	"shift_state", "inside_temp_c", "outside_temp_c", "climate_on", "charge_state", "charger_power_kw",
	"charger_voltage", "charger_amps", "charge_energy_added_kwh", "charge_limit_pct", "locked", "sentry_mode"}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Store) InsertTelemetry(ctx context.Context, vehicleID string, pts []models.TelemetryPoint) error {
	if len(pts) == 0 {
		return nil
	}
	rows := make([][]any, len(pts))
	for i, p := range pts {
		rows[i] = []any{p.Time, vehicleID, nullStr(p.State), p.BatteryPct, p.UsableBatteryPct, p.RatedRangeKm,
			p.IdealRangeKm, p.EstRangeKm, p.Lat, p.Lon, p.HeadingDeg, p.SpeedKph, p.PowerKW, p.OdometerKm,
			nullStr(p.ShiftState), p.InsideTempC, p.OutsideTempC, p.ClimateOn, nullStr(p.ChargeState), p.ChargerPowerKW,
			p.ChargerVoltage, p.ChargerAmps, p.ChargeEnergyAddedKWh, p.ChargeLimitPct, p.Locked, p.SentryMode}
	}
	if len(pts) == 1 {
		ph := make([]string, len(telemetryCols))
		for i := range ph {
			ph[i] = fmt.Sprintf("$%d", i+1)
		}
		_, err := s.pool.Exec(ctx, `INSERT INTO telemetry (`+strings.Join(telemetryCols, ",")+`) VALUES (`+strings.Join(ph, ",")+`)`, rows[0]...)
		return err
	}
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"telemetry"}, telemetryCols, pgx.CopyFromRows(rows))
	return err
}

func scanTelemetry(row pgx.Row) (models.TelemetryPoint, error) {
	var p models.TelemetryPoint
	var state, shift, charge *string
	err := row.Scan(&p.Time, &state, &p.BatteryPct, &p.UsableBatteryPct, &p.RatedRangeKm, &p.IdealRangeKm, &p.EstRangeKm,
		&p.Lat, &p.Lon, &p.HeadingDeg, &p.SpeedKph, &p.PowerKW, &p.OdometerKm, &shift, &p.InsideTempC, &p.OutsideTempC,
		&p.ClimateOn, &charge, &p.ChargerPowerKW, &p.ChargerVoltage, &p.ChargerAmps, &p.ChargeEnergyAddedKWh,
		&p.ChargeLimitPct, &p.Locked, &p.SentryMode)
	p.Time = p.Time.UTC()
	if state != nil {
		p.State = *state
	}
	if shift != nil {
		p.ShiftState = *shift
	}
	if charge != nil {
		p.ChargeState = *charge
	}
	return p, err
}

func (s *Store) QueryTelemetry(ctx context.Context, vehicleID string, from, to time.Time, bucket time.Duration, limit int) ([]models.TelemetryPoint, error) {
	if limit <= 0 {
		limit = 100_000
	}
	var rows pgx.Rows
	var err error
	if bucket <= 0 {
		rows, err = s.pool.Query(ctx, `SELECT `+strings.Join(telemetryCols[:1], ",")+`, `+strings.Join(telemetryCols[2:], ",")+`
			FROM telemetry WHERE vehicle_id=$1 AND time >= $2 AND time < $3 ORDER BY time LIMIT $4`, vehicleID, from, to, limit)
	} else {
		last := func(c string) string {
			return fmt.Sprintf("(array_agg(%[1]s ORDER BY time DESC) FILTER (WHERE %[1]s IS NOT NULL))[1]", c)
		}
		avg := func(c string) string { return "avg(" + c + ")" }
		cols := []string{
			"date_bin(make_interval(secs => $4::double precision), time, TIMESTAMPTZ '2000-01-01 00:00:00+00') AS b",
			last("state"), avg("battery_pct"), avg("usable_battery_pct"), avg("rated_range_km"), avg("ideal_range_km"),
			avg("est_range_km"), avg("lat"), avg("lon"), avg("heading_deg"), avg("speed_kph"), avg("power_kw"),
			avg("odometer_km"), last("shift_state"), avg("inside_temp_c"), avg("outside_temp_c"), last("climate_on"),
			last("charge_state"), avg("charger_power_kw"), avg("charger_voltage"), avg("charger_amps"),
			avg("charge_energy_added_kwh"), avg("charge_limit_pct"), last("locked"), last("sentry_mode"),
		}
		rows, err = s.pool.Query(ctx, `SELECT `+strings.Join(cols, ", ")+`
			FROM telemetry WHERE vehicle_id=$1 AND time >= $2 AND time < $3
			GROUP BY b ORDER BY b LIMIT $5`, vehicleID, from, to, bucket.Seconds(), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.TelemetryPoint{}
	for rows.Next() {
		p, err := scanTelemetry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- trips

const tripCols = `id::text, vehicle_id::text, started_at, ended_at, start_odometer_km, end_odometer_km, distance_km,
	duration_s, avg_speed_kph, max_speed_kph, start_battery_pct, end_battery_pct, start_range_km, end_range_km,
	energy_used_kwh, efficiency_wh_km, start_lat, start_lon, end_lat, end_lon, start_place, end_place, outside_temp_c`

func geoArgs(g *models.GeoPoint) (*float64, *float64) {
	if g == nil {
		return nil, nil
	}
	return &g.Lat, &g.Lon
}

func geoFrom(lat, lon *float64) *models.GeoPoint {
	if lat == nil || lon == nil {
		return nil
	}
	return &models.GeoPoint{Lat: *lat, Lon: *lon}
}

func scanTrip(row pgx.Row) (models.Trip, error) {
	var t models.Trip
	var sLat, sLon, eLat, eLon *float64
	err := row.Scan(&t.ID, &t.VehicleID, &t.StartedAt, &t.EndedAt, &t.StartOdometerKm, &t.EndOdometerKm, &t.DistanceKm,
		&t.DurationS, &t.AvgSpeedKph, &t.MaxSpeedKph, &t.StartBatteryPct, &t.EndBatteryPct, &t.StartRangeKm, &t.EndRangeKm,
		&t.EnergyUsedKWh, &t.EfficiencyWhKm, &sLat, &sLon, &eLat, &eLon, &t.StartPlace, &t.EndPlace, &t.OutsideTempC)
	if err != nil {
		return t, notFound(err)
	}
	t.StartedAt = t.StartedAt.UTC()
	if t.EndedAt != nil {
		e := t.EndedAt.UTC()
		t.EndedAt = &e
	}
	t.StartLocation, t.EndLocation = geoFrom(sLat, sLon), geoFrom(eLat, eLon)
	return t, nil
}

func (s *Store) SaveTrip(ctx context.Context, t *models.Trip) error {
	if t.ID == "" {
		t.ID = ids.New()
	}
	sLat, sLon := geoArgs(t.StartLocation)
	eLat, eLon := geoArgs(t.EndLocation)
	_, err := s.pool.Exec(ctx, `INSERT INTO trips (id, vehicle_id, started_at, ended_at, start_odometer_km, end_odometer_km,
		distance_km, duration_s, avg_speed_kph, max_speed_kph, start_battery_pct, end_battery_pct, start_range_km, end_range_km,
		energy_used_kwh, efficiency_wh_km, start_lat, start_lon, end_lat, end_lon, start_place, end_place, outside_temp_c)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		ON CONFLICT (id) DO UPDATE SET ended_at=EXCLUDED.ended_at, end_odometer_km=EXCLUDED.end_odometer_km,
		  distance_km=EXCLUDED.distance_km, duration_s=EXCLUDED.duration_s, avg_speed_kph=EXCLUDED.avg_speed_kph,
		  max_speed_kph=EXCLUDED.max_speed_kph, end_battery_pct=EXCLUDED.end_battery_pct, end_range_km=EXCLUDED.end_range_km,
		  energy_used_kwh=EXCLUDED.energy_used_kwh, efficiency_wh_km=EXCLUDED.efficiency_wh_km, end_lat=EXCLUDED.end_lat,
		  end_lon=EXCLUDED.end_lon, start_place=EXCLUDED.start_place, end_place=EXCLUDED.end_place,
		  outside_temp_c=EXCLUDED.outside_temp_c`,
		t.ID, t.VehicleID, t.StartedAt, t.EndedAt, t.StartOdometerKm, t.EndOdometerKm, t.DistanceKm, t.DurationS,
		t.AvgSpeedKph, t.MaxSpeedKph, t.StartBatteryPct, t.EndBatteryPct, t.StartRangeKm, t.EndRangeKm,
		t.EnergyUsedKWh, t.EfficiencyWhKm, sLat, sLon, eLat, eLon, t.StartPlace, t.EndPlace, t.OutsideTempC)
	return err
}

// listWhere builds the shared WHERE clause for time-ordered lists.
func listWhere(o store.ListOptions, args []any) (string, []any) {
	var conds []string
	if !o.From.IsZero() {
		args = append(args, o.From)
		conds = append(conds, fmt.Sprintf("started_at >= $%d", len(args)))
	}
	if !o.To.IsZero() {
		args = append(args, o.To)
		conds = append(conds, fmt.Sprintf("started_at < $%d", len(args)))
	}
	if o.Before != nil {
		args = append(args, *o.Before)
		conds = append(conds, fmt.Sprintf("started_at < $%d", len(args)))
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 10_000
	}
	args = append(args, limit)
	where := ""
	if len(conds) > 0 {
		where = " AND " + strings.Join(conds, " AND ")
	}
	return where + fmt.Sprintf(" ORDER BY started_at DESC LIMIT $%d", len(args)), args
}

func (s *Store) ListTrips(ctx context.Context, vehicleID string, o store.ListOptions) ([]models.Trip, error) {
	tail, args := listWhere(o, []any{vehicleID})
	rows, err := s.pool.Query(ctx, `SELECT `+tripCols+` FROM trips WHERE vehicle_id=$1`+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Trip{}
	for rows.Next() {
		t, err := scanTrip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTrip(ctx context.Context, vehicleID, id string) (*models.Trip, error) {
	t, err := scanTrip(s.pool.QueryRow(ctx, `SELECT `+tripCols+` FROM trips WHERE vehicle_id=$1 AND id=$2`, vehicleID, id))
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------------------------------------------------------------- charging sessions

const chargeCols = `id::text, vehicle_id::text, started_at, ended_at, duration_s, start_battery_pct, end_battery_pct,
	start_range_km, end_range_km, energy_added_kwh, max_power_kw, avg_power_kw, fast_charger, charger_type,
	lat, lon, place, cost, currency, end_reason`

func scanCharge(row pgx.Row) (models.ChargeSession, error) {
	var c models.ChargeSession
	var lat, lon *float64
	err := row.Scan(&c.ID, &c.VehicleID, &c.StartedAt, &c.EndedAt, &c.DurationS, &c.StartBatteryPct, &c.EndBatteryPct,
		&c.StartRangeKm, &c.EndRangeKm, &c.EnergyAddedKWh, &c.MaxPowerKW, &c.AvgPowerKW, &c.FastCharger, &c.ChargerType,
		&lat, &lon, &c.Place, &c.Cost, &c.Currency, &c.EndReason)
	if err != nil {
		return c, notFound(err)
	}
	c.StartedAt = c.StartedAt.UTC()
	if c.EndedAt != nil {
		e := c.EndedAt.UTC()
		c.EndedAt = &e
	}
	c.Location = geoFrom(lat, lon)
	return c, nil
}

func (s *Store) SaveChargeSession(ctx context.Context, c *models.ChargeSession) error {
	if c.ID == "" {
		c.ID = ids.New()
	}
	lat, lon := geoArgs(c.Location)
	_, err := s.pool.Exec(ctx, `INSERT INTO charging_sessions (id, vehicle_id, started_at, ended_at, duration_s,
		start_battery_pct, end_battery_pct, start_range_km, end_range_km, energy_added_kwh, max_power_kw, avg_power_kw,
		fast_charger, charger_type, lat, lon, place, cost, currency, end_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (id) DO UPDATE SET ended_at=EXCLUDED.ended_at, duration_s=EXCLUDED.duration_s,
		  end_battery_pct=EXCLUDED.end_battery_pct, end_range_km=EXCLUDED.end_range_km,
		  energy_added_kwh=EXCLUDED.energy_added_kwh, max_power_kw=EXCLUDED.max_power_kw, avg_power_kw=EXCLUDED.avg_power_kw,
		  fast_charger=EXCLUDED.fast_charger, charger_type=EXCLUDED.charger_type, lat=EXCLUDED.lat, lon=EXCLUDED.lon,
		  place=EXCLUDED.place, cost=EXCLUDED.cost, currency=EXCLUDED.currency, end_reason=EXCLUDED.end_reason`,
		c.ID, c.VehicleID, c.StartedAt, c.EndedAt, c.DurationS, c.StartBatteryPct, c.EndBatteryPct, c.StartRangeKm,
		c.EndRangeKm, c.EnergyAddedKWh, c.MaxPowerKW, c.AvgPowerKW, c.FastCharger, c.ChargerType, lat, lon, c.Place,
		c.Cost, c.Currency, c.EndReason)
	return err
}

func (s *Store) ListChargeSessions(ctx context.Context, vehicleID string, o store.ListOptions) ([]models.ChargeSession, error) {
	tail, args := listWhere(o, []any{vehicleID})
	rows, err := s.pool.Query(ctx, `SELECT `+chargeCols+` FROM charging_sessions WHERE vehicle_id=$1`+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ChargeSession{}
	for rows.Next() {
		c, err := scanCharge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChargeSession(ctx context.Context, vehicleID, id string) (*models.ChargeSession, error) {
	c, err := scanCharge(s.pool.QueryRow(ctx, `SELECT `+chargeCols+` FROM charging_sessions WHERE vehicle_id=$1 AND id=$2`, vehicleID, id))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ---------------------------------------------------------------- drain

func (s *Store) InsertDrain(ctx context.Context, d models.DrainSegment) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO idle_drain (vehicle_id, started_at, ended_at, range_lost_km, energy_kwh)
		VALUES ($1,$2,$3,$4,$5)`, d.VehicleID, d.StartedAt, d.EndedAt, d.RangeLostKm, d.EnergyKWh)
	return err
}

func (s *Store) ListDrain(ctx context.Context, vehicleID string, from, to time.Time) ([]models.DrainSegment, error) {
	rows, err := s.pool.Query(ctx, `SELECT vehicle_id::text, started_at, ended_at, range_lost_km, energy_kwh FROM idle_drain
		WHERE vehicle_id=$1 AND ended_at >= $2 AND started_at < $3 ORDER BY started_at`, vehicleID, from, to)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (models.DrainSegment, error) {
		var d models.DrainSegment
		err := r.Scan(&d.VehicleID, &d.StartedAt, &d.EndedAt, &d.RangeLostKm, &d.EnergyKWh)
		d.StartedAt, d.EndedAt = d.StartedAt.UTC(), d.EndedAt.UTC()
		return d, err
	})
}

// ---------------------------------------------------------------- events

func (s *Store) InsertEvent(ctx context.Context, e *models.Event) error {
	payload, _ := json.Marshal(e.Payload)
	if e.Payload == nil {
		payload = []byte("{}")
	}
	return s.pool.QueryRow(ctx, `INSERT INTO events (vehicle_id, type, title, body, occurred_at, payload)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, e.VehicleID, e.Type, e.Title, e.Body, e.OccurredAt, payload).Scan(&e.ID)
}

func (s *Store) ListEvents(ctx context.Context, userID, vehicleID string, limit int, beforeID int64) ([]models.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id, e.vehicle_id::text, e.type, e.title, e.body, e.occurred_at, e.payload, e.delivered_at
		FROM events e JOIN user_vehicles uv ON uv.vehicle_id = e.vehicle_id AND uv.user_id = $1
		WHERE ($2 = '' OR e.vehicle_id::text = $2) AND ($3 = 0 OR e.id < $3)
		ORDER BY e.id DESC LIMIT $4`, userID, vehicleID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (models.Event, error) {
		var e models.Event
		var payload []byte
		err := r.Scan(&e.ID, &e.VehicleID, &e.Type, &e.Title, &e.Body, &e.OccurredAt, &payload, &e.DeliveredAt)
		e.OccurredAt = e.OccurredAt.UTC()
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &e.Payload)
		}
		return e, err
	})
}

func (s *Store) MarkEventDelivered(ctx context.Context, id int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE events SET delivered_at=$2 WHERE id=$1`, id, at)
	return err
}

// ---------------------------------------------------------------- rules

func (s *Store) GetRules(ctx context.Context, userID string) ([]models.Rule, error) {
	rows, err := s.pool.Query(ctx, `SELECT type, enabled, params FROM notification_rules WHERE user_id=$1 ORDER BY type`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (models.Rule, error) {
		var rule models.Rule
		var params []byte
		err := r.Scan(&rule.Type, &rule.Enabled, &params)
		if len(params) > 0 {
			_ = json.Unmarshal(params, &rule.Params)
		}
		if len(rule.Params) == 0 {
			rule.Params = nil
		}
		return rule, err
	})
}

func (s *Store) SaveRules(ctx context.Context, userID string, rules []models.Rule) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM notification_rules WHERE user_id=$1`, userID); err != nil {
			return err
		}
		for _, r := range rules {
			params, _ := json.Marshal(r.Params)
			if r.Params == nil {
				params = []byte("{}")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO notification_rules (user_id, type, enabled, params) VALUES ($1,$2,$3,$4)`,
				userID, r.Type, r.Enabled, params); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------- geofences

const geofenceCols = `id::text, name, lat, lon, radius_m, energy_rate_per_kwh`

func scanGeofence(row pgx.Row) (models.Geofence, error) {
	var g models.Geofence
	err := row.Scan(&g.ID, &g.Name, &g.Lat, &g.Lon, &g.RadiusM, &g.EnergyRatePerKWh)
	return g, notFound(err)
}

func (s *Store) ListGeofences(ctx context.Context, userID string) ([]models.Geofence, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+geofenceCols+` FROM geofences WHERE user_id=$1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (models.Geofence, error) { return scanGeofence(r) })
}

func (s *Store) GetGeofence(ctx context.Context, userID, id string) (*models.Geofence, error) {
	g, err := scanGeofence(s.pool.QueryRow(ctx, `SELECT `+geofenceCols+` FROM geofences WHERE user_id=$1 AND id=$2`, userID, id))
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) CreateGeofence(ctx context.Context, userID string, g *models.Geofence) error {
	return s.pool.QueryRow(ctx, `INSERT INTO geofences (user_id, name, lat, lon, radius_m, energy_rate_per_kwh)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`, userID, g.Name, g.Lat, g.Lon, g.RadiusM, g.EnergyRatePerKWh).Scan(&g.ID)
}

func (s *Store) UpdateGeofence(ctx context.Context, userID string, g *models.Geofence) error {
	tag, err := s.pool.Exec(ctx, `UPDATE geofences SET name=$3, lat=$4, lon=$5, radius_m=$6, energy_rate_per_kwh=$7
		WHERE user_id=$1 AND id=$2`, userID, g.ID, g.Name, g.Lat, g.Lon, g.RadiusM, g.EnergyRatePerKWh)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteGeofence(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM geofences WHERE user_id=$1 AND id=$2`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- push devices

func (s *Store) UpsertPushDevice(ctx context.Context, userID string, d *models.PushDevice) error {
	return s.pool.QueryRow(ctx, `INSERT INTO push_devices (user_id, token, platform, environment) VALUES ($1,$2,$3,$4)
		ON CONFLICT (token) DO UPDATE SET user_id=EXCLUDED.user_id, platform=EXCLUDED.platform, environment=EXCLUDED.environment
		RETURNING id::text, created_at`, userID, d.Token, d.Platform, d.Environment).Scan(&d.ID, &d.CreatedAt)
}

func (s *Store) ListPushDevices(ctx context.Context, userID string) ([]models.PushDevice, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, token, platform, environment, created_at FROM push_devices
		WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (models.PushDevice, error) {
		var d models.PushDevice
		err := r.Scan(&d.ID, &d.Token, &d.Platform, &d.Environment, &d.CreatedAt)
		d.CreatedAt = d.CreatedAt.UTC()
		return d, err
	})
}

func (s *Store) DeletePushDevice(ctx context.Context, userID, token string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM push_devices WHERE user_id=$1 AND token=$2`, userID, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeletePushToken(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_devices WHERE token=$1`, token)
	return err
}

// ---------------------------------------------------------------- idempotency & audit

func (s *Store) GetIdempotency(ctx context.Context, userID, key string) (*store.IdempotencyRecord, error) {
	var r store.IdempotencyRecord
	err := s.pool.QueryRow(ctx, `SELECT user_id::text, key, request_hash, status_code, body, created_at FROM idempotency_keys
		WHERE user_id=$1 AND key=$2 AND created_at > now() - interval '24 hours'`, userID, key).
		Scan(&r.UserID, &r.Key, &r.RequestHash, &r.StatusCode, &r.Body, &r.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

func (s *Store) SaveIdempotency(ctx context.Context, r *store.IdempotencyRecord) error {
	_, _ = s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < now() - interval '24 hours'`)
	_, err := s.pool.Exec(ctx, `INSERT INTO idempotency_keys (user_id, key, request_hash, status_code, body, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, r.UserID, r.Key, r.RequestHash, r.StatusCode, r.Body, r.CreatedAt)
	if isUniqueViolation(err) {
		return store.ErrConflict
	}
	return err
}

func (s *Store) InsertCommandLog(ctx context.Context, l *store.CommandLog) error {
	if l.ID == "" {
		l.ID = ids.New()
	}
	params, _ := json.Marshal(l.Params)
	var uid *string
	if l.UserID != "" {
		uid = &l.UserID
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO command_log (id, vehicle_id, user_id, name, params, status, reason, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, l.ID, l.VehicleID, uid, l.Name, params, l.Status, l.Reason, l.CreatedAt)
	return err
}
