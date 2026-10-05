# iOS App Architecture (SwiftUI — flagship)

## Module layout (Swift Package / Xcode targets)
- **App (Xcode target `TeslaCloneApp`)** — SwiftUI entry, scene, weight/deep-link routing.
- **CoreKit (package)** — networking, auth/session, models, WebSocket client, caching,
  concurrency primitives. Shared and reusable; the typed API client mirrors `docs/api.md`.
- **LiveKit** — live-status state machine (drives via WebSocket deltas), vehicle store.
- **AnalyticsKit** — chart data sources (Swift Charts), date bucketing, formatting.
- **CommandsKit** — command request/response + optimistic UI + rollback.
- **NotificationsKit** — APNs registration, background handling, deep links (push → vehicle/dash).
- **WidgetKit extension** — live battery/charge widget (timeline provider polling the API or
  using shared app-group data).

## Networking / API client
- `APIClient` (async/await) + `APIRouter` (endpoints from `api.md`).
- **Real-time:** WebSocket client (`URLSessionWebSocketTask`) subscribing to
  `/v1/vehicles/{id}/ws`; merge deltas into `LiveState`.
- Decode via `Codable` types generated from the server contract (or OpenAPI-generated Swift).
- **Concurrency:** `@MainActor` view models; `ObservableObject`/`@Observable` stores;
  background updates via `BGTaskScheduler`; widget via `WidgetCenter`.

## Key screens
1. **Vehicles list** — thumbnails, live battery/range, charge state, last seen.
2. **Vehicle dashboard** — top status bar (battery/range/climate/charge), live map, quick
   actions (climate, lock, trunk, charge, sentry).
3. **Controls / Commands** — grouped command surface with optimistic updates + error/rollback.
4. **Charging** — current session, curve chart, history list.
5. **Trips** — recent trips, detail with efficiency + route.
6. **Analytics** — day/week/month dashboard (efficiency, energy, distance, drain), range breakdown.
7. **Battery health** — capacity/degradation over time.
8. **Notifications / Rules** — event feed + rule toggles.
9. **Settings & Account** — plans/billing, vehicles, data export + delete, sign out.

## UX/responsiveness principles (matches the "Tessie-like" feel)
- Live state is **push-driven** (WebSocket) with a low-frequency refresh fallback — the app
  feels instant, not polled.
- Optimistic command UI: show intent immediately, reconcile with authoritative server deltas.
- Offline tolerance: cache last-known state (app-group + local store); show "last updated."
- No trusted client clock — display server timestamps.

## Push & background
- APNs registration; store token server-side (`POST /v1/push/devices`).
- Handle background refresh + silent pushes for state updates; WidgetKit timeline refresh.
- Deep links: `teslaclone://vehicle/{id}` → specific screen.

## App Store readiness (commercial)
- Privacy nutrition labels (location, telemetry, identifiers).
- Account deletion + data-export flow (API + UI).
- StoreKit subscriptions (plans `free`/`pro`), restore purchases.
- TestFlight seed + analytics/telemetry consent prompts.

## Android (follow-on, reuses same API)
- Kotlin + Jetpack Compose; same websocket-driven live state; FCM push; same backend contract.
- Essentially a new UI skin over the versioned public API.
