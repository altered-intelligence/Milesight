# HANDOFF

## Goal of this chat
Build **Milesight** — an iOS-native (SwiftUI) Tesla companion app with "Tessie-like"
features (fleet monitoring, battery/charging analytics, trip logging, remote commands,
notifications, dashboards). Follow-on: Android. GitHub: altered-intelligence/Milesight.

## Decisions made
- Product name Milesight (ADR-008). Backend Go (ADR-001); Postgres + TimescaleDB (ADR-005).
- Official Tesla **Fleet API** for prod, Owner API for dev, behind `TeslaTransport`
  (ADR-002/003). Streaming-first WebSocket (ADR-004). Tessie features, not branding (ADR-007).
- iOS first, Android follow-on. Repo is public; description + README set.

## Files created / changed (scaffolding phase)
- `backend/` — Go server skeleton: `cmd/server`, `internal/{config,api,models,transport/
  owner|fleet,store/postgres,realtime}` + `go.mod`, `Makefile`, `docker-compose.yml`,
  `.env.example`. **Not compiled** (Go not installed here).
- `ios/Packages/` — SwiftPM kits CoreKit/LiveKit/AnalyticsKit/CommandsKit/NotificationsKit.
  All **build**; CoreKit tests **pass** (built with `--disable-sandbox`; `.build/` gitignored).
- `ios/App/` — SwiftUI app target sources + `ios/project.yml` (XcodeGen).
- `README.md`, `docs/*` updated. `HANDOFF.md` = this file.

## Build caveats in this environment
- Go/Docker not installed → backend unverified. `go mod tidy` needed to produce go.sum.
- Swift needs `--disable-sandbox` + scratch/cache paths inside `ios/.build` to compile here
  (sandbox restricts `/var/folders` clang cache and SwiftPM manifest sandbox-exec).

## Open questions
- Apply for Tesla **Fleet API** dev access (longest lead time — start now).
- Auth/JWT (Bearer) is still a stub on both ends; Owner-API dev auth not implemented.
- Xcode app target not generated yet (needs XcodeGen or manual .xcodeproj).
- OAuth/consent flow, billing/plans, privacy labels not started.

## Exact next step
Implement real **auth** (Owner API dev SSO; Fleet OAuth scaffold) + wire `GET /v1/vehicles`
and `/v1/vehicles/{id}/live` end-to-end (Go handler → transport → store), then connect the
iOS `LiveVehicleStore` to those endpoints. Also run `go mod tidy` once Go is installed.
