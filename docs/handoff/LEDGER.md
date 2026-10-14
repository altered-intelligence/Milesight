# Work ledger

Single source of truth for what is planned, built and proven. Protocol and status legend:
[`HANDOFF.md`](../../HANDOFF.md). **Only `TESTED`/`VERIFIED` count as done.**

Evidence commands assume `cd backend` and the environment in [`ENVIRONMENT.md`](ENVIRONMENT.md).
`PG` = run with `MILESIGHT_TEST_DATABASE_URL` set (otherwise those tests skip).

_Last updated: S01 (2026-10-14, Claude Opus 5.5) — mid-session._

## Backend (Go)

| ID | Item | Status | Evidence | Notes |
|---|---|---|---|---|
| BE-01 | API contract types, SI units, snake_case (`internal/models`) | TESTED | exercised by store/analytics/tesla tests | ADR-009 |
| BE-02 | Config load + validation (`internal/config`) | IMPLEMENTED | — | needs unit tests |
| BE-03 | Store interface + in-memory store | TESTED | `go test ./internal/store/memory` (conformance suite) | |
| BE-04 | Postgres store + embedded migrations | TESTED | PG: `go test ./internal/store/postgres` (same conformance suite, PG 16) | Timescale branch of 0002 **untested** (no Timescale here) |
| BE-05 | Auth: HS256 JWT, AES-GCM token cipher, PKCE, refresh rotation + reuse detection | TESTED | `go test ./internal/auth` | ADR-014 |
| BE-06 | Tesla REST client + error mapping (401/404/408/412/421/429) | TESTED | `go test ./internal/tesla` (fake Fleet server) | |
| BE-07 | `vehicle_data` → LiveState mapping | TESTED | `TestToSnapshotMapsUnitsAndStates` | fixture written from docs/field knowledge, **not a captured real response** — verify against a real car |
| BE-08 | Tesla OAuth (Fleet authorize/exchange/refresh; Owner refresh) | IMPLEMENTED | refresh path covered by `TestCredentialsRefreshIsSingleFlight` | authorize + code exchange never run against Tesla |
| BE-09 | Encrypted credential store, single-flight token refresh | TESTED | `TestCredentialsRefreshIsSingleFlight` | |
| BE-10 | Fleet transport (VIN-keyed, regional, command proxy) | TESTED (fake) | `TestFleetTransport*` | **BLOCKED** for real verification: needs Tesla developer app + partner registration |
| BE-11 | Owner transport (dev) | TESTED (fake) | `TestOwnerTransportAddressesByID` | Tesla restricts Owner API; may not work for real cars |
| BE-12 | Vehicle simulator transport (live loop, commands, 30-day history) | TESTED | `go test ./internal/sim` | ADR-010 |
| BE-13 | Analytics: trip/charge segmentation, drain | TESTED | `go test ./internal/analytics` incl. `TestSimulatedMonth` | |
| BE-14 | Daily/weekly/monthly rollups (tz-aware) | TESTED | `TestRollupBucketsAndDrainSplit` | |
| BE-15 | Battery health estimate | TESTED | `TestBatteryHealth`, `TestSimulatedMonth` | |
| BE-16 | Realtime hub (typed messages, watcher counts) | PLANNED | | |
| BE-17 | Ingestion manager (sleep/cost-aware polling, history seeding, poke after command) | PLANNED | | ADR-013 |
| BE-18 | Notification rule engine + event log | PLANNED | | |
| BE-19 | APNs pusher (token auth, ES256) + log pusher | PLANNED | | real APNs needs Apple key — BLOCKED for verification |
| BE-20 | Commands service (validation, wake-then-send, rate limit, audit) | PLANNED | | |
| BE-21 | HTTP API — every route in `docs/api.md` | PLANNED | | |
| BE-22 | WebSocket endpoint (state/event messages, auth via header or query) | PLANNED | | |
| BE-23 | Idempotency-Key handling for mutating requests | PLANNED | store part TESTED | |
| BE-24 | CSV export (trips, charges) | PLANNED | | |
| BE-25 | Account deletion endpoint | PLANNED | store part TESTED (`delete_user`) | App Store requirement |
| BE-26 | Server main, graceful shutdown, Dockerfile, docker-compose | PLANNED | | |
| BE-27 | End-to-end API test (simulator + memory store) | PLANNED | | |
| BE-28 | Live smoke test of running server incl. WebSocket | PLANNED | | |
| BE-29 | Fleet Telemetry streaming ingestion (self-hosted fleet-telemetry) | BACKLOG | | needed for cost at scale (ADR-013) |
| BE-30 | Partner registration tooling (public key at `/.well-known/...`, `partner_accounts`) | BACKLOG | | |
| BE-31 | Timescale continuous aggregates | BACKLOG | | |
| BE-32 | Billing / StoreKit server validation | BACKLOG | | |
| BE-33 | Horizontal scaling (Redis or Postgres-based work distribution) | BACKLOG | | ADR-015 |

## iOS (SwiftUI)

| ID | Item | Status | Evidence | Notes |
|---|---|---|---|---|
| IOS-01 | CoreKit models + JSON coding matching the contract | PLANNED | | original scaffold had snake/camel and date-decoding mismatches |
| IOS-02 | Authenticated API client (refresh serialization) + Keychain | PLANNED | | |
| IOS-03 | Router covering all endpoints | PLANNED | | |
| IOS-04 | WebSocket client | PLANNED | | |
| IOS-05 | LiveKit vehicle store | PLANNED | | |
| IOS-06 | CommandsKit (optimistic + rollback) | PLANNED | | |
| IOS-07 | AnalyticsKit (unit formatting, chart series) | PLANNED | | |
| IOS-08 | NotificationsKit (APNs registration, deep links) | PLANNED | | |
| IOS-09 | Sign-in (dev/simulator login; Tesla OAuth via ASWebAuthenticationSession) | PLANNED | | |
| IOS-10 | Screens: vehicles, dashboard, controls, charging, trips, analytics, battery, alerts, places, settings | PLANNED | | |
| IOS-11 | XcodeGen spec / Info.plist (URL scheme, push) | PLANNED | | committed `.xcodeproj` is stale; regenerate |
| IOS-12 | Kit unit tests | PLANNED | | |
| IOS-13 | WidgetKit, App Intents, StoreKit | BACKLOG | | |

## Docs, CI, delivery

| ID | Item | Status | Evidence | Notes |
|---|---|---|---|---|
| DOC-01 | Remove all Tessie references; reword | TESTED | `grep -rni tessie .` → no matches (excl. `.git`) | older commits still contain it |
| DOC-02 | Handoff protocol + ledger + session logs | IMPLEMENTED | this file | keep updated |
| DOC-03 | `docs/api.md` matches implemented contract | PLANNED | | |
| DOC-04 | ADRs 009–015 | PLANNED | | |
| DOC-05 | README build/run instructions | PLANNED | | |
| OPS-01 | GitHub Actions: Go tests + Postgres service + macOS iOS build | PLANNED | | first proof that iOS compiles |
| OPS-02 | Push S01 work to GitHub | BLOCKED | | GitHub not linked to Claude in S01; bundle delivered instead |
| AND-01 | Android client | BACKLOG | | |
