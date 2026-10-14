-- Milesight core schema (PostgreSQL 14+).
-- Units are SI (ADR-009). Locations are plain lat/lon columns (ADR-011, no PostGIS).
-- TimescaleDB features are layered on in 0002 only when the extension is available (ADR-012).

-- ------------------------------------------------------------------ users & auth
CREATE TABLE users (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id  TEXT NOT NULL UNIQUE,                 -- "tesla:<sub>" | "dev:<email>"
    email        TEXT NOT NULL DEFAULT '',
    plan         TEXT NOT NULL DEFAULT 'free',          -- free | pro
    status       TEXT NOT NULL DEFAULT 'active',
    transport    TEXT NOT NULL DEFAULT '',              -- fleet | owner | sim
    preferences  JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tesla_credentials (                        -- AES-256-GCM encrypted by the app
    user_id            UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    transport          TEXT NOT NULL,
    access_token_enc   BYTEA NOT NULL,
    refresh_token_enc  BYTEA NOT NULL,
    scopes             TEXT[] NOT NULL DEFAULT '{}',
    region             TEXT NOT NULL DEFAULT 'na',
    expires_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE refresh_tokens (                           -- rotating, hashed, reuse-detecting
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id   UUID NOT NULL,
    token_hash  BYTEA NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX refresh_tokens_family ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_user ON refresh_tokens (user_id);

CREATE TABLE auth_states (                              -- pending OAuth authorizations
    state          TEXT PRIMARY KEY,
    transport      TEXT NOT NULL,
    region         TEXT NOT NULL DEFAULT 'na',
    redirect_uri   TEXT NOT NULL,
    code_verifier  TEXT NOT NULL DEFAULT '',
    app_redirect   TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL
);

-- ------------------------------------------------------------------ vehicles
CREATE TABLE vehicles (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vin              TEXT NOT NULL UNIQUE,
    tesla_id         TEXT NOT NULL DEFAULT '',
    display_name     TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    trim             TEXT NOT NULL DEFAULT '',
    color            TEXT NOT NULL DEFAULT '',
    region           TEXT NOT NULL DEFAULT 'na',
    state            TEXT NOT NULL DEFAULT 'unknown',
    sync_status      TEXT NOT NULL DEFAULT 'idle',
    last_seen        TIMESTAMPTZ,
    latest           JSONB,                             -- last LiveState snapshot
    processor_state  JSONB,                             -- ingestion/analytics cursor
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_vehicles (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    vehicle_id  UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'owner',          -- owner | viewer
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, vehicle_id)
);
CREATE INDEX user_vehicles_vehicle ON user_vehicles (vehicle_id);

-- ------------------------------------------------------------------ telemetry (hypertable when Timescale is present)
CREATE TABLE telemetry (
    time                     TIMESTAMPTZ NOT NULL,
    vehicle_id               UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    state                    TEXT,
    battery_pct              DOUBLE PRECISION,
    usable_battery_pct       DOUBLE PRECISION,
    rated_range_km           DOUBLE PRECISION,
    ideal_range_km           DOUBLE PRECISION,
    est_range_km             DOUBLE PRECISION,
    lat                      DOUBLE PRECISION,
    lon                      DOUBLE PRECISION,
    heading_deg              DOUBLE PRECISION,
    speed_kph                DOUBLE PRECISION,
    power_kw                 DOUBLE PRECISION,
    odometer_km              DOUBLE PRECISION,
    shift_state              TEXT,
    inside_temp_c            DOUBLE PRECISION,
    outside_temp_c           DOUBLE PRECISION,
    climate_on               BOOLEAN,
    charge_state             TEXT,
    charger_power_kw         DOUBLE PRECISION,
    charger_voltage          DOUBLE PRECISION,
    charger_amps             DOUBLE PRECISION,
    charge_energy_added_kwh  DOUBLE PRECISION,
    charge_limit_pct         DOUBLE PRECISION,
    locked                   BOOLEAN,
    sentry_mode              BOOLEAN
);
CREATE INDEX telemetry_vehicle_time ON telemetry (vehicle_id, time DESC);

-- ------------------------------------------------------------------ derived history
CREATE TABLE trips (
    id                 UUID PRIMARY KEY,
    vehicle_id         UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    started_at         TIMESTAMPTZ NOT NULL,
    ended_at           TIMESTAMPTZ,
    start_odometer_km  DOUBLE PRECISION NOT NULL DEFAULT 0,
    end_odometer_km    DOUBLE PRECISION NOT NULL DEFAULT 0,
    distance_km        DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_s         INTEGER NOT NULL DEFAULT 0,
    avg_speed_kph      DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_speed_kph      DOUBLE PRECISION NOT NULL DEFAULT 0,
    start_battery_pct  INTEGER NOT NULL DEFAULT 0,
    end_battery_pct    INTEGER NOT NULL DEFAULT 0,
    start_range_km     DOUBLE PRECISION NOT NULL DEFAULT 0,
    end_range_km       DOUBLE PRECISION NOT NULL DEFAULT 0,
    energy_used_kwh    DOUBLE PRECISION NOT NULL DEFAULT 0,
    efficiency_wh_km   DOUBLE PRECISION,
    start_lat          DOUBLE PRECISION,
    start_lon          DOUBLE PRECISION,
    end_lat            DOUBLE PRECISION,
    end_lon            DOUBLE PRECISION,
    start_place        TEXT NOT NULL DEFAULT '',
    end_place          TEXT NOT NULL DEFAULT '',
    outside_temp_c     DOUBLE PRECISION
);
CREATE INDEX trips_vehicle_started ON trips (vehicle_id, started_at DESC);

CREATE TABLE charging_sessions (
    id                 UUID PRIMARY KEY,
    vehicle_id         UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    started_at         TIMESTAMPTZ NOT NULL,
    ended_at           TIMESTAMPTZ,
    duration_s         INTEGER NOT NULL DEFAULT 0,
    start_battery_pct  INTEGER NOT NULL DEFAULT 0,
    end_battery_pct    INTEGER NOT NULL DEFAULT 0,
    start_range_km     DOUBLE PRECISION NOT NULL DEFAULT 0,
    end_range_km       DOUBLE PRECISION NOT NULL DEFAULT 0,
    energy_added_kwh   DOUBLE PRECISION NOT NULL DEFAULT 0,
    max_power_kw       DOUBLE PRECISION NOT NULL DEFAULT 0,
    avg_power_kw       DOUBLE PRECISION NOT NULL DEFAULT 0,
    fast_charger       BOOLEAN NOT NULL DEFAULT false,
    charger_type       TEXT NOT NULL DEFAULT '',
    lat                DOUBLE PRECISION,
    lon                DOUBLE PRECISION,
    place              TEXT NOT NULL DEFAULT '',
    cost               DOUBLE PRECISION,
    currency           TEXT NOT NULL DEFAULT '',
    end_reason         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX charging_sessions_vehicle_started ON charging_sessions (vehicle_id, started_at DESC);

CREATE TABLE idle_drain (                               -- vampire drain while parked
    vehicle_id     UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    started_at     TIMESTAMPTZ NOT NULL,
    ended_at       TIMESTAMPTZ NOT NULL,
    range_lost_km  DOUBLE PRECISION NOT NULL,
    energy_kwh     DOUBLE PRECISION NOT NULL
);
CREATE INDEX idle_drain_vehicle_started ON idle_drain (vehicle_id, started_at);

-- ------------------------------------------------------------------ notifications, places, devices
CREATE TABLE events (
    id            BIGSERIAL PRIMARY KEY,
    vehicle_id    UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    type          TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    body          TEXT NOT NULL DEFAULT '',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload       JSONB NOT NULL DEFAULT '{}',
    delivered_at  TIMESTAMPTZ
);
CREATE INDEX events_vehicle_id ON events (vehicle_id, id DESC);

CREATE TABLE notification_rules (
    user_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type     TEXT NOT NULL,
    enabled  BOOLEAN NOT NULL,
    params   JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (user_id, type)
);

CREATE TABLE geofences (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    lat                  DOUBLE PRECISION NOT NULL,
    lon                  DOUBLE PRECISION NOT NULL,
    radius_m             DOUBLE PRECISION NOT NULL DEFAULT 150,
    energy_rate_per_kwh  DOUBLE PRECISION,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX geofences_user ON geofences (user_id);

CREATE TABLE push_devices (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token        TEXT NOT NULL UNIQUE,
    platform     TEXT NOT NULL DEFAULT 'ios',
    environment  TEXT NOT NULL DEFAULT 'production',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX push_devices_user ON push_devices (user_id);

-- ------------------------------------------------------------------ idempotency + audit
CREATE TABLE idempotency_keys (
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    status_code   INTEGER NOT NULL,
    body          BYTEA NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);

CREATE TABLE command_log (
    id          UUID PRIMARY KEY,
    vehicle_id  UUID NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    name        TEXT NOT NULL,
    params      JSONB NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX command_log_vehicle ON command_log (vehicle_id, created_at DESC);
