# Milesight — Tesla Companion App (Product & Engineering Plan)

A working plan for a commercial Tesla companion app reproducing the core of Tessie:
real-time fleet monitoring, deep battery/charging analytics, automatic trip logging,
remote commands, notifications, and dashboards.

**Shipping target: iOS native first (SwiftUI). Android (Kotlin/Compose) as a follow-on.**

## Resolved decisions
- **Commercial product → official Tesla Fleet API** is the long-term target. Developer access
  is applied for early (it has lead time). Owner API is used only as a dev/bootstrap fallback,
  behind a pluggable transport abstraction.
- **Backend: Go** — for responsiveness (concurrency, real-time streaming, low latency, many
  concurrent client connections) and a single static deployable binary. TypeScript is a
  viable alternative if you want one typed language across backend + web.
- **Live-data architecture: streaming-first.** Fleet API streaming telemetry + WebSocket push
  to clients, so the app is reactive rather than poll-based.
- **Scope is "Tessie-like features," not its branding/UI.** Review Tesla's third-party dev terms.

Scoped in phases so you can ship a usable MVP, then add analytics. Supporting docs:
- `docs/decisions.md` — recorded decisions + rationale
- `docs/api.md` — public client API design
- `docs/schema.sql` — Postgres + Timescale schema / migrations
- `docs/ios-architecture.md` — iOS app structure

---

## 1. Product Scope (feature map)

### Tier 1 — MVP (core loop)
- **Vehicle auth & linking** — connect Tesla (Fleet OAuth), list vehicles, active vehicle.
- **Real-time status** — battery %, projected range, charge state, location, climate,
  lock/charge port/windows/doors/trunk state, software version, odometer.
- **Remote commands** — climate on/off + set temp, lock/unlock, frunk/trunk/charge port,
  start/stop charging, flash lights/honk.
- **Push notifications** — charge complete, charging started/stopped, climate, sentry alerts,
  software update available, geofence arrival/departure.

### Tier 2 — Historical data & telemetry pipeline
- Continuous time-series telemetry (battery, range, temp, location, speed, power, odometer).
- Charge history + per-session charging curves.
- Automatic trip logging (distance, duration, energy, efficiency Wh/mi, route).
- Efficiency / "ghost" graph and battery-health estimate.

### Tier 3 — Deep analytics & dashboards (Tessie-style)
- Range & efficiency dashboards by day/week/month; temp/speed correlation.
- Charging cost estimator (configurable rates + location rules).
- Sleep/awake & vampire-drain tracking.
- Scorecards (best/worst, totals, cross-vehicle comparison).
- iOS WidgetKit widgets + App Intents/Shortcuts.

### Tier 4 — Multi-user / commercial polish
- Accounts, plans/billing, multi-vehicle, roles.
- Cloud sync/backup, data export, audit + retention controls, compliance.

---

## 2. Tesla API Strategy (commercial)

### Primary: Official Fleet API
- **Onboarding (start Week 1 — has lead time):** Tesla developer portal →
  register an app → enable **Fleet API** → select scopes. Requires business/legal identity,
  so kick off immediately.
- **Auth:** OAuth 2.0 with your client credentials; per-owner consent granting scoped access
  to their vehicles (this replaces the old SSO flow for commercial use).
- **Data:** vehicle product/status endpoints + commands, and **Streaming telemetry**
  (WebSocket/SSE) that pushes live measurements — this is how we achieve the responsive,
  reactive UX without hammering the API.
- **Regional note:** Fleet API is region-partitioned (e.g., North America vs. EU). Payloads
  and endpoints differ slightly by region; keep region configurable.

### Fallback (dev/bootstrap only): Legacy Owner API
- Simple Tesla SSO → tokens, `/api/1/...` endpoints. Fine for a personal dev vehicle and for
  prototyping while Fleet approval is pending. Not the commercial transport.

### Transport abstraction
- The ingestion layer talks to a `TeslaTransport` interface with two adapters
  (`FleetTransport`, `OwnerTransport`). All downstream code (telemetry store, aggregation,
  commands, notifications) is transport-agnostic. Swapping = config change, not rewrite.

---

## 3. Architecture

```
 Tesla Fleet API (streaming) ──► [Ingestion service (Go)]
        │                              │  writes measurements + emits events
        ▼                              ▼
 [Streaming telemetry]            [Time-series store]  (TimescaleDB)
                                        │ (materialized trips, sessions, daily rolls)
                                        ▼
                              [Notification / rule engine] ──► [APNs / FCM]
                                        ▼
                              [Public API gateway (Grpc-Gateway/HTTP2, WebSocket)]
                                        │
        ┌───────────────────────────────┼──────────────────────────┐
        ▼                               ▼                          ▼
   iOS app (SwiftUI)               Web (Next.js, optional)     Android (Kotlin, later)
```

### Backend components (Go monolith → split later)
1. **Auth service** — Fleet OAuth (client creds + per-user consent), token refresh/rotation,
   encrypted token store, scopes management.
2. **Ingestion/realtime** — streaming consumer + fallback poller; sleep/wake lifecycle
   (never keep cars awake); writes telemetry and emits domain events.
3. **Telemetry store** — append-only time-series (TimescaleDB hypertable).
4. **Aggregation worker** — segments time-series into trips, charging sessions, daily rolls.
5. **Command service** — validated, idempotent, rate-limited remote commands.
6. **Notification service** — rule engine + push (APNs now, FCM later).
7. **Public API** — gRPC + HTTP/2 (or REST) + WebSocket for live updates; versioned.

### Client strategy (iOS-first)
- **iOS (SwiftUI)** is the flagship: live tiles, Swift Charts dashboards, commands,
  notifications, WidgetKit, App Intents. Concurrency via async/await + Combine.
- **Web (Next.js)** — optional ops/dashboard surface.
- **Android (Compose)** — follow-on reusing the same versioned public API.

---

## 4. Data Model

Full DDL in `docs/schema.sql`. Summary:

### Relational (Postgres)
- `users` — email, auth method, encrypted Tesla tokens, roles, plan.
- `vehicles` — Tesla vehicle ID, VIN, display name, config snapshot (model/trim/software),
  region.
- `user_vehicles` — membership + permissions (many-to-many).
- `charging_sessions` — vehicle, start/end, start/end %, kWh added, cost, location, max power.
- `trips` — vehicle, start/end, start/end odometer, distance, duration, avg speed,
  energy_used_kwh, efficiency_wh_per_mi, start/end geo, route ref.
- `daily_stats` — per-vehicle/day rollups.
- `events` — notification log (type, occurred_at, payload, delivered_at).
- `geofences` — user places for location rules.
- `plans` / `subscriptions` — billing when commercial.

### Time-series (Timescale hypertable `telemetry`)
- timestamp, vehicle_id, battery_level, rated/ideal/est ranges, lat/lon, speed, power_kw,
  odometer, inside/outside temp, hvac_state, charge_state, charge_amps/voltage/kw, drive
  state, door/lock/window/trunk/charge-port booleans, sentry, software_update.

### Derived logic
- **Trip detection:** contiguous "driving" period (drive state + odometer increase) bounded by
  parked/stable-location states.
- **Charging session:** contiguous "not Disconnected" period; split on charge-port open/close
  and location change.
- **Battery health:** infer usable capacity from repeated charging sessions (Δ% vs energy) vs
  rated capacity; smooth over time.

---

## 5. Recommended Tech Stack

| Layer | Choice | Notes |
|---|---|---|
| Backend | **Go** | responsiveness, concurrency, streaming, single binary |
| Tesla transport | Pluggable: Fleet + Owner adapters | Owner = dev fallback |
| Database | PostgreSQL | entities |
| Time-series | TimescaleDB | hypertables + continuous aggregates |
| Queue / jobs | Redis (streams/consumer groups) | aggregation, notifications |
| Realtime | WebSocket (gRPC-Web or JSON) | live updates to clients |
| Push | APNs now; FCM later | |
| Client (iOS) | SwiftUI + Swift Charts + WidgetKit + App Intents | flagship |
| Client (Android later) | Kotlin + Jetpack Compose | |
| Web (optional) | Next.js + Recharts | dashboards/ops |
| Infra | Docker Compose dev → Fly.io/Railway/AWS prod | autoscale realtime |

---

## 6. Milestones (iOS-first, commercial-aware)

- **Week 1 — Foundation:** apply for Fleet API access; scaffold Go backend + Postgres/Timescale
  (Docker Compose); Owner-API transport for dev; list vehicles + live status in iOS app.
- **Weeks 2–3 — MVP:** commands; sleeping poller; charge-complete notifications; iOS live
  tiles, command buttons, notification permission, settings.
- **Weeks 4–6 — Telemetry & analytics:** full ingestion; trip + charging segmentation; charge
  history + efficiency graph; battery-health estimate.
- **Weeks 6–8 — Dashboards & polish:** day/week/month dashboards (Swift Charts), widgets
  (WidgetKit), Shortcuts; cost estimator; geofences; vampire-drain report; export; multi-vehicle.
- **Weeks 9+ — Fleet API migration + Android + commercial:** swap to Fleet transport +
  streaming; Android client; billing/plans, multi-user auth, permissions, cloud sync,
  reliability/alerting, App Store compliance, audit/retention.

---

## 7. Risks & Constraints (commercial emphasis)

- **Fleet API approval delay** — apply day one; prototype on Owner API meanwhile under the
  abstraction.
- **Tesla ToS/limits** — respect scopes, poll/stream responsibly, stay sleep-friendly.
- **Vehicle sleep** — never wake cars unnecessarily; defer to on-demand wake.
- **Secrets** — tokens/client secrets encrypted at rest; never in the client; rotate.
- **Privacy/compliance** — vehicles + precise location are sensitive. Consent, retention
  limits, export/delete, HTTPS-only, min data collection for App Store review.
- **Apple App Store** — license/agreements page, privacy nutrition labels, subscription
  billing (StoreKit), account deletion requirement.
- **Legal** — replicate features, not branding/assets; review Tesla third-party dev terms;
  trademark/logo usage.
- **Infra cost** — telemetry volume + realtime connections; Timescale compression + data
  lifecycle policies to control cost.

---

## 8. Suggested Immediate Next Steps
1. **Apply for Tesla Fleet API developer access** (longest lead time — start now).
2. Scaffold Go backend with Docker Compose (Postgres + Timescale + Redis) and Owner-API
   transport for dev.
3. Implement auth (Owner for dev, Fleet OAuth scaffold for prod) + list vehicles + live status.
4. Stand up the iOS SwiftUI app with a typed API client + WebSocket live updates.
5. Add the telemetry write path + first aggregation (trip/charging) as a proof of the pipeline.
