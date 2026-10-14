# Work ledger

Single source of truth for what is planned, built and proven. Protocol and status legend:
[`HANDOFF.md`](../../HANDOFF.md). **Only `TESTED`/`VERIFIED` count as done.**

Evidence commands assume `cd backend` and the environment in [`ENVIRONMENT.md`](ENVIRONMENT.md).
`PG` = run with `MILESIGHT_TEST_DATABASE_URL` set (otherwise those tests skip).

_Last updated: end of S01 (2026-10-14, Claude Opus 5.5)._

## Backend (Go)

| ID | Item | Status | Evidence | Notes |
|---|---|---|---|---|
| BE-01 | API contract types, SI units, snake_case (`internal/models`) | TESTED | exercised by store/analytics/tesla tests | ADR-009 |
| BE-02 | Config load + validation (`internal/config`) | TESTED | `go test ./internal/config` |  |
| BE-03 | Store interface + in-memory store | TESTED | `go test ./internal/store/memory` (conformance suite) | |
| BE-04 | Postgres store + embedded migrations | TESTED | PG: `go test ./internal/store/postgres` on PG 16 (local) and TimescaleDB (CI, with `MILESIGHT_EXPECT_TIMESCALE=1` asserting the hypertable) |  |
| BE-05 | Auth: HS256 JWT, AES-GCM token cipher, PKCE, refresh rotation + reuse detection | TESTED | `go test ./internal/auth` | ADR-014 |
| BE-06 | Tesla REST client + error mapping (401/404/408/412/421/429) | TESTED | `go test ./internal/tesla` (fake Fleet server) | |
| BE-07 | `vehicle_data` → LiveState mapping | TESTED | `TestToSnapshotMapsUnitsAndStates` | fixture written from docs/field knowledge, **not a captured real response** — verify against a real car |
| BE-08 | Tesla OAuth (Fleet authorize/exchange/refresh; Owner refresh) | IMPLEMENTED | refresh path covered by `TestCredentialsRefreshIsSingleFlight`; authorize/redirect/callback exercised only on the disabled path | BLOCKED for real verification: needs a Tesla developer app (client id/secret, registered redirect URI) |
| BE-09 | Encrypted credential store, single-flight token refresh | TESTED | `TestCredentialsRefreshIsSingleFlight` | |
| BE-10 | Fleet transport (VIN-keyed, regional, command proxy) | TESTED (fake) | `TestFleetTransport*` | **BLOCKED** for real verification: needs Tesla developer app + partner registration |
| BE-11 | Owner transport (dev) | TESTED (fake) | `TestOwnerTransportAddressesByID` | Tesla restricts Owner API; may not work for real cars |
| BE-12 | Vehicle simulator transport (live loop, commands, 30-day history) | TESTED | `go test ./internal/sim` | ADR-010 |
| BE-13 | Analytics: trip/charge segmentation, drain | TESTED | `go test ./internal/analytics` incl. `TestSimulatedMonth` | |
| BE-14 | Daily/weekly/monthly rollups (tz-aware) | TESTED | `TestRollupBucketsAndDrainSplit` | |
| BE-15 | Battery health estimate | TESTED | `TestBatteryHealth`, `TestSimulatedMonth` | |
| BE-16 | Realtime hub (typed messages, watcher counts) | TESTED | `go test ./internal/realtime` |  |
| BE-17 | Ingestion manager (sleep/cost-aware polling, history seeding, poke after command) | TESTED | `TestPipelineWithSimulator` (3 simulated hours through the real poll loop); live smoke on Postgres | ADR-013; real-Tesla cadence/cost unverified |
| BE-18 | Notification rule engine + event log | TESTED | `go test ./internal/notify` (hysteresis, left open/unlocked, geofence, rendering, per-user delivery) |  |
| BE-19 | APNs pusher (token auth, ES256) + log pusher | TESTED (fake) | `TestAPNsSignsAndSends` verifies the ES256 provider token and 410 pruning | real APNs needs an Apple key — BLOCKED for verification |
| BE-20 | Commands service (validation, wake-then-send, rate limit, audit) | TESTED | `go test ./internal/commands` + e2e |  |
| BE-21 | HTTP API — every route in `docs/api.md` | TESTED | `TestEndToEnd` (every route family) + live smoke | Fleet OAuth authorize/callback only exercised on the disabled path |
| BE-22 | WebSocket endpoint (state/event messages, auth via header or query) | VERIFIED | e2e WebSocket test + live probe (101, initial state, state + command stream) |  |
| BE-23 | Idempotency-Key handling for mutating requests | TESTED | e2e: replay header; 422 on a reused key with a different body |  |
| BE-24 | CSV export (trips, charges) | TESTED | e2e CSV parse; live smoke |  |
| BE-25 | Account deletion endpoint | TESTED | e2e: delete account → 401; store conformance | App Store requirement |
| BE-26 | Server main, graceful shutdown, Dockerfile, docker-compose | TESTED | server binary run live (S01); Docker image builds in CI | `docker compose up` itself not run yet |
| BE-27 | End-to-end API test (simulator + memory store) | TESTED | `go test -race -count=10 -run TestEndToEnd ./internal/app` ×4, all pass | found + fixed a simulator data race and a seeding visibility race |
| BE-28 | Live smoke test of running server incl. WebSocket | VERIFIED | S01: server on Postgres 16 — dev login, stats, health, commands (wake path), idempotent replay, WS probe, CSV export, zero ERROR log lines |  |
| BE-29 | Fleet Telemetry streaming ingestion (self-hosted fleet-telemetry) | BACKLOG | | needed for cost at scale (ADR-013) |
| BE-30 | Partner registration tooling (public key at `/.well-known/...`, `partner_accounts`) | BACKLOG | | |
| BE-31 | Timescale continuous aggregates | BACKLOG | | |
| BE-32 | Billing / StoreKit server validation | BACKLOG | | |
| BE-33 | Horizontal scaling (Redis or Postgres-based work distribution) | BACKLOG | | ADR-015 |
| BE-34 | Per-user API rate limiting / abuse protection (beyond per-vehicle command limit) | BACKLOG | | |
| BE-35 | Observability: metrics (poll counts → Tesla cost), tracing, alerting | BACKLOG | | Fleet API cost visibility matters (ADR-013) |
| BE-36 | Capture a real `vehicle_data` response and add it as a mapping fixture | BLOCKED | | needs a real car on Fleet/Owner API (see BE-07) |
| BE-37 | Viewer/sharing flow (invite another user as `viewer`) | BACKLOG | | roles exist in data model and are enforced |

## iOS (SwiftUI)

| ID | Item | Status | Evidence | Notes |
|---|---|---|---|---|
| IOS-01 | CoreKit models + JSON coding matching the contract | TESTED | CI `swift test` (CoreKit): `ContractTests` decode 19 real server fixtures | fixtures: `go run ./cmd/fixtures` |
| IOS-02 | Authenticated API client (refresh serialization) + Keychain | TESTED | CI: `APIClientTests` (coalesced refresh, sign-out on failed refresh, 409 refusal) | Keychain store compiled, not exercised in tests |
| IOS-03 | Router covering all endpoints | TESTED | CI: `CodingTests.testEndpointURLs` + contract tests |  |
| IOS-04 | WebSocket client | IMPLEMENTED | compiles in CI; envelope decoding tested (`testWebSocketEnvelope`) | live socket never run against the server from Swift |
| IOS-05 | LiveKit vehicle store | TESTED | CI: `VehicleStoreTests` (ordering, overlays, trip tracking) | streaming/reconnect loop untested |
| IOS-06 | CommandsKit (optimistic + rollback) | TESTED | CI: `VehicleCommandTests` (bodies match contract, optimistic effects) |  |
| IOS-07 | AnalyticsKit (unit formatting, chart series) | TESTED | CI: `SeriesTests` |  |
| IOS-08 | NotificationsKit (APNs registration, deep links) | TESTED | CI: `DeepLinkTests` | APNs registration needs a device + Apple key |
| IOS-09 | Sign-in (dev/simulator login; Tesla OAuth via ASWebAuthenticationSession) | IMPLEMENTED | app compiles in CI (unsigned simulator build) | never run in Simulator; Tesla OAuth blocked on developer app |
| IOS-10 | Screens: vehicles, dashboard, controls, charging, trips, analytics, battery, alerts, places, settings | IMPLEMENTED | app compiles in CI | **UI never run** — first manual QA pass is the top iOS next step (IOS-14) |
| IOS-11 | XcodeGen spec / Info.plist (URL scheme, push) | TESTED | CI: `xcodegen generate` + `xcodebuild` succeed | set DEVELOPMENT_TEAM to run on device |
| IOS-12 | Kit unit tests | TESTED | CI: `swift test` for all five kits |  |
| IOS-13 | WidgetKit, App Intents, StoreKit | BACKLOG | | |
| IOS-14 | First manual QA pass: run app in Simulator against `go run ./cmd/server`, walk every screen | PLANNED | | **top iOS next step**; record findings in the ledger |
| IOS-15 | App icon, asset catalog, accent color asset, launch screen polish | BACKLOG | | app builds without an icon today |
| IOS-16 | Offline cache of last-known state (app group, used by widgets later) | BACKLOG | | |
| IOS-17 | UI tests (XCUITest) for sign-in → dashboard → command → drives | BACKLOG | | needs IOS-14 first |
| IOS-18 | Accessibility + Dynamic Type audit; iPad layout | BACKLOG | | |

## Docs, CI, delivery

| ID | Item | Status | Evidence | Notes |
|---|---|---|---|---|
| DOC-01 | Remove all references to a third-party companion app's name; reword | TESTED | the removed term (see the `docs/` diff in commit `edb8927`) has no matches in the working tree | older commits still contain it; never reintroduce it |
| DOC-02 | Handoff protocol + ledger + session logs | TESTED | HANDOFF.md protocol + ledger + S01 session log in repo | keep updated every session |
| DOC-03 | `docs/api.md` matches implemented contract | TESTED | contract fixtures + Swift contract tests enforce it | update api.md in the same change as any API change |
| DOC-04 | ADRs 009–015 | TESTED | `docs/decisions.md` ADR-009…015 |  |
| DOC-05 | README build/run instructions | TESTED | README rewritten |  |
| OPS-01 | GitHub Actions: Go tests + Postgres service + macOS iOS build | TESTED | GitHub Actions runs 38522527833 and 38522889133 green (both jobs) |  |
| OPS-02 | Push S01 work to GitHub | TESTED | S01 pushed to `s01-build-out` and fast-forwarded to `main` |  |
| OPS-03 | Branch protection on `main` requiring CI | BACKLOG | | repo setting (owner) |
| OPS-04 | Deploy target (Fly.io/Railway/AWS) + managed Timescale; secrets management | BACKLOG | | |
| AND-01 | Android client | BACKLOG | | |
