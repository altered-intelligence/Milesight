# Milesight

A Tesla companion app with "Tessie-like" features — real-time fleet monitoring, deep battery
& charging analytics, automatic trip logging, remote commands, notifications, and dashboards.

## Status
Planning/documentation phase. iOS native (SwiftUI) first; Android (Kotlin/Compose) as a follow-on.

## Highlights
- **Real-time fleet monitoring** across all your vehicles.
- **Battery & charging analytics** — deep insights into charge curves, efficiency, and range.
- **Automatic trip logging** — no manual tracking.
- **Remote commands** and **notifications** from a clean dashboard.

## Architecture
- **Transports:** production uses the official Tesla **Fleet API**; the legacy Owner API is
  used only for dev/bootstrap, both behind a pluggable `TeslaTransport` abstraction.
- **Backend:** Go (streaming-first: Fleet streaming telemetry + WebSocket push over polling).
- **Data:** PostgreSQL for entities + TimescaleDB for telemetry hypertables.

## Docs
- `docs/plan.md` — product & engineering plan
- `docs/decisions.md` — ADR log (product name, transports, backend, storage)
- `docs/api.md` — public API spec
- `docs/schema.sql` — data model
- `docs/ios-architecture.md` — iOS architecture notes

## License
TBD

## Project structure
- `backend/` — Go API server (contract-first, mirrors `docs/api.md`):
  `cmd/server`, `internal/api`, `internal/models`, `internal/transport`
  (`owner`/`fleet` adapters), `internal/store`, `internal/realtime` (WebSocket hub).
  Includes `docker-compose.yml` (TimescaleDB + Redis) and `Makefile`.
- `ios/Packages/` — SwiftPM kits: `CoreKit` (typed API client + models + WebSocket),
  `LiveKit` (live vehicle store), `AnalyticsKit` (Swift Charts data), `CommandsKit`
  (optimistic commands), `NotificationsKit` (push/deep links).
- `ios/App/` — SwiftUI app target (generate with XcodeGen via `ios/project.yml`).
- `docs/` — plan, ADRs, API spec, schema, iOS architecture.

## Build
- Backend: `cd backend && cp .env.example .env && go run ./cmd/server` (requires Go + Docker).
- iOS packages (validate standalone): `cd ios/Packages/CoreKit && swift build` then
  `swift test`. Generate the app project: `brew install xcodegen && cd ios && xcodegen generate`.
