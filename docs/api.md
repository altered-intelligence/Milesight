# Public Client API (contract-first, versioned)

Base URL pattern: `https://api.yourdomain.com/v1` (WebSocket at `wss://api.yourdomain.com/v1/ws`).
Format: JSON over HTTPS. Auth: `Authorization: Bearer <jwt>`.
GraphQL/HTTP2 (gRPC-gateway) is an option; JSON/REST shown for clarity. **Protobuf or OpenAPI
spec should be generated from the Go types so all clients share one contract.**

## Auth
- `POST /v1/auth/tesla/authorize` — start Tesla (Owner or Fleet) OAuth, return redirect + state.
- `POST /v1/auth/tesla/callback` — exchange code; create/refresh session; returns JWT + refresh.
- `POST /v1/auth/refresh` — rotate refresh token.
- `DELETE /v1/auth/session` — sign out; revoke consent + delete cloud data (App Store req.).

## Vehicles
- `GET /v1/vehicles` — list vehicles the user can access (+ sync status, last seen).
- `GET /v1/vehicles/{id}` — full current state snapshot (aggregate of all sub-states).
- `PATCH /v1/vehicles/{id}` — display name, preferences.
- `POST /v1/vehicles/{id}/wake` — wake the car (on demand).

## Live status (real-time)
- `GET /v1/vehicles/{id}/live` — current values: battery %, ranges, charge state, location,
  climate, locks/windows/doors/trunk/charge port, odometer, software.
- `WS /v1/vehicles/{id}/ws` (or `/v1/ws` with subscription) — push deltas for live state,
  drive events, charge events, alerts. This is how the app stays responsive without polling.

## Commands (validated, idempotent, rate-limited)
- `POST /v1/vehicles/{id}/commands/climate` `{on, temp_c}`
- `POST /v1/vehicles/{id}/commands/lock` `{}`
- `POST /v1/vehicles/{id}/commands/unlock` `{}`
- `POST /v1/vehicles/{id}/commands/frunk` · `trunk` · `charge_port` · `charge_port_close`
- `POST /v1/vehicles/{id}/commands/charging` `{action: start|stop|set_amps, amps?}`
- `POST /v1/vehicles/{id}/commands/honk` · `flash_lights`
- All return `{request_id, status, vehicle_state_delta?}`; commands are async — client
  observes the resulting state via live status/WS.

## Telemetry & analytics
- `GET /v1/vehicles/{id}/telemetry` `?from&to&fields&resolution` — raw/bucketed time-series.
- `GET /v1/vehicles/{id}/charges` — list charging sessions (paginated).
- `GET /v1/vehicles/{id}/charges/{session_id}` — session detail + charging curve.
- `GET /v1/vehicles/{id}/trips` — list trips (paginated).
- `GET /v1/vehicles/{id}/trips/{trip_id}` — trip detail with route/efficiency.
- `GET /v1/vehicles/{id}/stats/daily|weekly|monthly?from&to` — rollups (energy, distance,
  efficiency, drain).
- `GET /v1/vehicles/{id}/battery/health` — estimated capacity/degradation.

## Places & rules
- `GET|POST|PATCH|DELETE /v1/geofences` — user places; `{name, lat, lon, radius_m}`.
- `GET|POST /v1/rules` — notification rules (event type → on/off, place scoping, thresholds).

## Notifications
- `GET|POST|DELETE /v1/push/devices` — register APNs device token(s).
- `GET /v1/events` — historical notification/event log.

## Billing (commercial)
- `GET /v1/billing/plan` · `POST /v1/billing/subscribe` · `PATCH /v1/billing/cancel`

## Client versioning & compatibility
- `/v1` is stable; additive changes only within a minor. Breaking changes bump to `/v2`.
- All endpoints return structured errors: `{error: {code, message, request_id}}`.
- **Convention:** server is authoritative for time/second ordering (client clock not trusted).
