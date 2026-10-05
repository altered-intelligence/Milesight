-- Tesla clone — Postgres (entities) + Timescale (telemetry) schema
-- Timescale required for hypertables/continuous aggregates/compression.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- ------------------------------------------------------------------ USERS/AUTH
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    plan            TEXT NOT NULL DEFAULT 'free',           -- free | pro
    status          TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE user_credentials (                              -- Tesla tokens, encrypted
    user_id         UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    tesla_token_enc BYTEA NOT NULL,                          -- encrypt at rest
    refresh_token_enc BYTEA NOT NULL,
    oauth_scopes    TEXT[] NOT NULL DEFAULT '{}',
    region          TEXT NOT NULL DEFAULT 'na',              -- na | eu | ... (Fleet partitioning)
    expires_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ------------------------------------------------------------------ VEHICLES
CREATE TABLE vehicles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tesla_vehicle_id TEXT NOT NULL,                          -- Tesla-side vehicle id
    vin             TEXT UNIQUE NOT NULL,
    display_name    TEXT,
    config          JSONB NOT NULL DEFAULT '{}',             -- model, trim, software, etc.
    region          TEXT NOT NULL DEFAULT 'na',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_vehicles (                                 -- membership/permissions
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    vehicle_id      UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    role            TEXT NOT NULL DEFAULT 'owner',           -- owner | viewer
    PRIMARY KEY (user_id, vehicle_id)
);

-- ------------------------------------------------------------------ CHARGING
CREATE TABLE charging_sessions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vehicle_id        UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    started_at        TIMESTAMPTZ NOT NULL,
    ended_at          TIMESTAMPTZ,
    start_pct         NUMERIC(5,2),
    end_pct           NUMERIC(5,2),
    energy_added_kwh  NUMERIC(8,2),
    cost              NUMERIC(10,4),                         -- from rate rules
    location          GEOGRAPHY(POINT),
    max_power_kw      NUMERIC(8,2),
    energy_rate_name  TEXT
);
CREATE INDEX ON charging_sessions (vehicle_id, started_at DESC);

-- ------------------------------------------------------------------ TRIPS
CREATE TABLE trips (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vehicle_id         UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    started_at         TIMESTAMPTZ NOT NULL,
    ended_at           TIMESTAMPTZ,
    start_odometer_mi  NUMERIC(10,2),
    end_odometer_mi    NUMERIC(10,2),
    distance_mi        NUMERIC(10,2),
    duration_s         INTEGER,
    avg_speed_mph      NUMERIC(8,2),
    energy_used_kwh    NUMERIC(8,2),
    efficiency_wh_per_mi NUMERIC(8,2),
    start_geo          GEOGRAPHY(POINT),
    end_geo            GEOGRAPHY(POINT),
    source             TEXT NOT NULL DEFAULT 'auto'
);
CREATE INDEX ON trips (vehicle_id, started_at DESC);

-- ------------------------------------------------------------------ ROLLUPS / EVENTS / PLACES
CREATE TABLE daily_stats (
    vehicle_id      UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    day             DATE NOT NULL,
    distance_mi     NUMERIC(12,2) NOT NULL DEFAULT 0,
    energy_used_kwh NUMERIC(12,2) NOT NULL DEFAULT 0,
    efficiency_wh_per_mi NUMERIC(8,2),
    park_drain_kwh  NUMERIC(8,2) NOT NULL DEFAULT 0,
    PRIMARY KEY (vehicle_id, day)
);

CREATE TABLE events (
    id           BIGSERIAL PRIMARY KEY,
    vehicle_id   UUID REFERENCES vehicles(id) ON DELETE CASCADE,
    type         TEXT NOT NULL,                              -- charge_complete, sentry, geofence...
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload      JSONB NOT NULL DEFAULT '{}',
    delivered_at TIMESTAMPTZ
);
CREATE INDEX ON events (vehicle_id, occurred_at DESC);

CREATE TABLE geofences (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    location   GEOGRAPHY(POINT) NOT NULL,
    radius_m   INTEGER NOT NULL DEFAULT 200
);

-- ------------------------------------------------------------------ TELEMETRY (Timescale)
CREATE TABLE telemetry (
    time                 TIMESTAMPTZ NOT NULL,
    vehicle_id           UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    battery_level        NUMERIC(5,2),
    rated_range_mi       NUMERIC(8,2),
    ideal_range_mi       NUMERIC(8,2),
    est_battery_range_mi NUMERIC(8,2),
    latitude             DOUBLE PRECISION,
    longitude            DOUBLE PRECISION,
    speed_mph            NUMERIC(8,2),
    power_kw             NUMERIC(8,2),
    odometer_mi          NUMERIC(12,2),
    inside_temp_c        NUMERIC(5,2),
    outside_temp_c       NUMERIC(5,2),
    hvac_state           TEXT,
    is_climate_on        BOOLEAN,
    charge_state         TEXT,                               -- Charging | Complete | Disconnected
    charge_amps          NUMERIC(6,2),
    charge_voltage       NUMERIC(6,2),
    charge_kw            NUMERIC(8,2),
    drive_state          TEXT,                               -- driving | parked
    is_locked            BOOLEAN,
    doors_open           BOOLEAN,
    windows_open         BOOLEAN,
    trunk_open           BOOLEAN,
    charge_port_open     BOOLEAN,
    sentry_mode          BOOLEAN,
    software_update_avail BOOLEAN
);

SELECT create_hypertable('telemetry', 'time', if_not_exists => TRUE);
CREATE INDEX ON telemetry (vehicle_id, time DESC);

-- Continuous aggregations → cheap dashboards (from telemetry directly).
-- Efficiency (Wh/mi) is derived per-trip in the trips table, so it isn't aggregated here.
CREATE MATERIALIZED VIEW telemetry_hourly WITH (timescaledb.continuous) AS
SELECT time_bucket('1 hour', time) AS bucket,
       vehicle_id,
       avg(battery_level) AS avg_battery,
       avg(speed_mph)     AS avg_speed,
       max(speed_mph)     AS max_speed,
       avg(power_kw)      AS avg_power
  FROM telemetry
GROUP BY bucket, vehicle_id;

-- Compression + retention (cost control for commercial)
ALTER TABLE telemetry SET (timescaledb.compress, timescaledb.compress_segmentby = 'vehicle_id');
SELECT add_compression_policy('telemetry', INTERVAL '7 days');
SELECT add_retention_policy('telemetry', INTERVAL '2 years');   -- align with your retention decision
