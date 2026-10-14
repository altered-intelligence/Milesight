# Milesight

A Tesla companion app: real-time vehicle status, remote commands, automatic trip and charging
logs, cost tracking, efficiency and battery-health insights, and smart notifications.

- **iOS app** (SwiftUI, iOS 17+) — the product.
- **Go backend** — talks to Tesla, stores history, runs analytics, pushes live updates over
  WebSocket and notifications over APNs.
- **Android** (Kotlin/Compose) is planned against the same API; a web dashboard is optional.

> New here — human or AI? Read [`HANDOFF.md`](HANDOFF.md) first. Work is tracked item by item in
> [`docs/handoff/LEDGER.md`](docs/handoff/LEDGER.md); only items marked TESTED/VERIFIED are done.

## Try it in two minutes (no Tesla account needed)

```sh
cd backend
go run ./cmd/server          # simulator + in-memory store on :8080
```
Then either open the iOS app in the Simulator (below) and tap **Start demo**, or use the API:
```sh
curl -s -X POST localhost:8080/v1/auth/dev/login -d '{"email":"me@example.com"}'
```
Each demo account gets two simulated cars with a month of drives and charging history; the cars
keep driving, charging and sleeping in real time and respond to commands.

## iOS app

```sh
brew install xcodegen
cd ios && xcodegen generate && open Milesight.xcodeproj
```
Run the **Milesight** scheme on an iPhone simulator. Debug builds point at `http://localhost:8080`
(change the server on the sign-in screen). To run on a device, set your team under Signing.

Screens: sign-in · vehicle dashboard (battery, range, live drive/charge, quick actions, climate,
map) · controls · drives (route map, speed) · charging (power curve, cost) · insights
(daily/weekly/monthly) · battery health · alerts and notification rules · places · settings.

## Backend

| | |
|---|---|
| Run with Postgres | `cd backend && docker compose up --build` (TimescaleDB + API) |
| Tests | `go test ./...` · Postgres suite: set `MILESIGHT_TEST_DATABASE_URL` (see `Makefile`) |
| Config | every variable is documented in [`backend/.env.example`](backend/.env.example) |
| API contract | [`docs/api.md`](docs/api.md) |

Transports (ADR-003): `sim` (default, development), `owner` (legacy Owner API, development only),
`fleet` (official Tesla Fleet API, production). Production needs a Tesla developer app, a
registered partner domain with your public key, a virtual key paired to each car, and Tesla's
vehicle-command proxy for signed commands (`TESLA_COMMAND_PROXY_URL`).

## Repository layout
```
backend/            Go service
  cmd/server        API server binary
  cmd/fixtures      captures real API responses as iOS contract-test fixtures
  internal/         api · auth · analytics · commands · ingest · notify · realtime · sim
                    store (memory + postgres) · tesla (Fleet/Owner REST, OAuth) · models
ios/
  App/              SwiftUI app target
  Packages/         CoreKit (models, API client, WebSocket) · LiveKit (vehicle store)
                    CommandsKit · AnalyticsKit · NotificationsKit
  project.yml       XcodeGen spec
docs/               plan · decisions (ADRs) · api · iOS architecture · handoff/
```

## Status
See [`HANDOFF.md`](HANDOFF.md) §2 for the current snapshot and
[`docs/handoff/LEDGER.md`](docs/handoff/LEDGER.md) for item-level status and evidence.

## Notes
Milesight is not affiliated with Tesla, Inc. Tesla and its marks belong to Tesla, Inc. Use of the
Tesla Fleet API is subject to Tesla's developer terms; API usage is billed by Tesla.

## License
TBD
