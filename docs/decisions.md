# Recorded Decisions (ADR-style log)

Status: Draft — confirm in review.

## ADR-001 — Backend language: Go
- **Context:** responsiveness was the top priority; need real-time streaming, high concurrency,
  low latency, and many concurrent client connections; deployable easily (static binary).
- **Decision:** Use Go for backend services (ingestion, aggregation, API).
- **Alternative:** TypeScript/Node — one typed language across backend + web; fine if type
  sharing with the web app matters more than raw concurrency.
- **Consequence:** Web client and backend drift on types unless you adopt a contract-first API
  (protobuf/OpenAPI) — addressed by the public API spec.

## ADR-002 — Commercial edge → Official Tesla Fleet API
- **Context:** the product is commercial; the legacy Owner API is not productized and is
  restricted at scale.
- **Decision:** Target Fleet API as the production transport. Owner API is dev/bootstrap only.
- **Consequence:** Requires Tesla developer account + app registration + per-owner consent;
  region-partitioned; apply early (lead time). Protect with a transport abstraction.

## ADR-003 — Transport abstraction
- **Decision:** All data/command flows sit behind a `TeslaTransport` interface with
  `FleetTransport` and `OwnerTransport` adapters. Swapping transports is config, not a rewrite.

## ADR-004 — Streaming-first realtime
- **Decision:** Prefer Fleet streaming telemetry + WebSocket push to clients over polling, for a
  reactive UX and to respect API limits. Owner-API fallback polls at a low, sleep-aware cadence.

## ADR-005 — Data storage
- **Decision:** PostgreSQL for entities + TimescaleDB for telemetry hypertables with continuous
  aggregates and compression/lifecycle policies to control cost.

## ADR-006 — Basic auth/dev path
- **Decision:** Bootstrapping uses Owner-API SSO for a personal dev vehicle; production OAuth is
  Fleet client-credentials + per-user consent. Roles/plans come with commercial billing.

## ADR-007 — Scope boundary
- **Decision:** Milesight's features, UX, branding and assets are original. Do not copy any
  other companion app's branding, logos, or copyrighted UI/assets. Review Tesla third-party
  dev terms (and Tesla trademark guidelines) before launch.

## ADR-008 — Product name: Milesight
- **Context:** needed an original product name that signals driving/trip analytics and
  telemetry without colliding with Tesla product branding or existing companion apps.
- **Decision:** Name the product **Milesight**. Runner-up was **Chargebook** (battery/charging
  angle) if the analytics focus shifts later.
- **Consequence:** Use "Milesight" for all branding, store listings, and code identifiers.

## ADR-009 — Canonical SI units in storage and API
- **Context:** Tesla reports miles/mph regardless of the car's settings; users want either unit system.
- **Decision:** Store and serve SI only (km, km/h, °C, kWh, kW, Wh/km). Clients convert using the
  user's `preferences`. Tesla values are converted once, deterministically, at ingestion.
- **Consequence:** One canonical history; no unit flags in the database; formatting lives in
  `CoreKit.UnitFormatter` (iOS) and `notify.Render` (push text).

## ADR-010 — Vehicle simulator transport
- **Context:** Fleet API access requires approval and real cars; analytics need weeks of data.
- **Decision:** Ship a physically plausible simulator (`internal/sim`) as a first-class transport:
  live demo loop, commands, sleep/wake, and 30 days of synthetic history seeded through the
  same ingestion/analytics path as real data.
- **Consequence:** The whole stack (and the iOS app) runs with zero external accounts; tests use
  it end to end. Simulator accounts are only offered when `MILESIGHT_DEV_LOGIN` is on.

## ADR-011 — Plain lat/lon columns, no PostGIS
- **Context:** The original schema used `GEOGRAPHY(POINT)`, which the TimescaleDB image does not
  provide; geospatial needs are small (geofence matching, trip endpoints).
- **Decision:** Store `lat`/`lon` doubles; do haversine matching in Go (`internal/geo`).
- **Consequence:** Works on any Postgres. Revisit if spatial queries over history are needed.

## ADR-012 — TimescaleDB is optional
- **Decision:** Embedded migrations; `0002_timescale.sql` enables hypertable, compression (7 d) and
  retention (2 y) only when the extension is preloaded. Vanilla Postgres works for dev/CI.
  Bucketed queries use `date_bin` (PG 14+), which works with or without Timescale.
- **Consequence:** CI runs the Timescale branch (TimescaleDB service); local dev may not.

## ADR-013 — Cost-aware, sleep-friendly ingestion
- **Context:** Fleet API bills per call; polling `vehicle_data` costs far more than streaming
  telemetry, and polling can keep cars awake.
- **Decision:** Per-vehicle workers poll by situation (driving 30 s, charging 60 s, active 60 s,
  idle 3 min, watched 20 s), suspend data reads after 12 min idle for 25 min so the car can sleep,
  only check state while asleep, and never wake implicitly (only on explicit user action/commands).
  Fleet Telemetry streaming is the production target (ledger BE-29).
- **Consequence:** Data freshness trades against cost; tune `transport.DefaultPolicy`.

## ADR-014 — Milesight-issued sessions; Tesla tokens encrypted at rest
- **Decision:** 15-minute HS256 JWT access tokens + opaque, hashed, rotating refresh tokens with
  reuse detection (reuse revokes the family). Tesla tokens are AES-256-GCM encrypted with the
  user id as associated data. Tesla refreshes are single-flight per user (Tesla refresh tokens are
  single-use). Clients must serialize refreshes too.
- **Consequence:** Tesla tokens never reach clients; a leaked database alone doesn't expose them.

## ADR-015 — No Redis yet
- **Decision:** Ingestion, notifications and rate limiting run in-process. Redis (or Postgres
  advisory-lock work distribution) is deferred until horizontal scaling (ledger BE-33).
- **Consequence:** Run a single API instance for now; command rate limits are per instance.
