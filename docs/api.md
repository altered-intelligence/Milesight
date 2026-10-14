# Milesight API v1 — contract

This document is the **implemented** contract (backend `internal/api`, types in
`internal/models`). The iOS client mirrors it in `ios/Packages/CoreKit`. If code and this
document disagree, the code wins and this document has a bug — fix it in the same change.

## Conventions
- Base URL: `https://<host>/v1` (local dev: `http://localhost:8080/v1`). JSON over HTTPS.
- **Keys are snake_case. Timestamps are RFC 3339 UTC** (`2026-10-14T06:08:55.527Z`).
- **Units are SI** (ADR-009): km, km/h, °C, kWh, kW, Wh/km. Clients convert for display
  using the user's `preferences`.
- Optional values are omitted (never `0` meaning "unknown").
- Auth: `Authorization: Bearer <access_token>` (15-minute JWT). Refresh with the refresh
  token; refresh tokens rotate and **must not be reused** (reuse revokes the session family —
  clients must serialize refreshes).
- Every response carries `X-Request-ID` (echoed if the client sends one).
- Errors: `{"error": {"code": "...", "message": "...", "request_id": "..."}}`.
- Mutating endpoints marked **idem** accept `Idempotency-Key: <≤128 chars>`. The first
  non-5xx response is stored 24 h and replayed (header `Idempotent-Replayed: true`); the
  same key with a different body → `422 idempotency_key_reused`.
- Lists are paginated with an opaque `cursor` → response `{"items": [...], "next_cursor": "..."}`
  (absent on the last page). `limit` defaults to 50 (max 200; events max 100).

### Error codes
| HTTP | code | meaning |
|---|---|---|
| 400 | `bad_request`, `invalid_json`, `invalid_command`, `invalid_preferences`, `invalid_geofence`, `invalid_rules`, `invalid_device`, `invalid_email`, `invalid_state`, `invalid_code`, `invalid_region` | input problems |
| 401 | `unauthorized` | missing/expired access token → refresh, then retry once |
| 401 | `invalid_refresh_token` | session over → sign in again |
| 403 | `forbidden` | viewer tried an owner-only action |
| 404 | `not_found`, `vehicle_not_found` | unknown or not yours (never leaks existence) |
| 409 | `tesla_reauth_required` | Tesla access expired → run "Sign in with Tesla" again |
| 409 | *(command result)* | the car refused a command; body is a `CommandResult` with `status:"failed"` |
| 422 | `idempotency_key_reused` | see above |
| 429 | `rate_limited`, `tesla_rate_limited` | honor `Retry-After` |
| 501 | `tesla_oauth_disabled`, `owner_login_disabled` | not configured on this server |
| 502 | `tesla_error` | upstream Tesla failure |
| 504 | `vehicle_unavailable`, `timeout` | car asleep/offline and didn't respond |

## Public
| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | `{"status":"ok"}` |
| GET | `/v1/config` | `ServerConfig`: which sign-in methods this server offers |

```json
{"version":"1.0.0","transport":"fleet","dev_login":false,"tesla_oauth":true,"owner_token_login":false,"regions":["na","eu","cn"]}
```

## Auth
| Method | Path | Body | Returns |
|---|---|---|---|
| POST | `/v1/auth/tesla/authorize` | `{"region":"na"}` (optional) | `{"authorize_url","state","redirect_uri","app_redirect"}` |
| GET | `/v1/auth/tesla/redirect` | — (Tesla's redirect target) | `302` to `app_redirect?code=…&state=…` |
| POST | `/v1/auth/tesla/callback` | `{"code","state"}` | `Session` |
| POST | `/v1/auth/dev/login` | `{"email"}` | `Session` (simulator accounts; only when `dev_login`) |
| POST | `/v1/auth/dev/tesla-token` | `{"refresh_token"}` | `Session` (Owner API dev path; only when `owner_token_login`) |
| POST | `/v1/auth/refresh` | `{"refresh_token"}` | `Session` (new refresh token every time) |
| DELETE | `/v1/auth/session` | `{"refresh_token"}` or `?all=true` | `204` |
| GET | `/v1/me` | — | `User` |
| PATCH | `/v1/me` | `{"preferences":{…partial}}` | `User` |
| DELETE | `/v1/account` | — | `{"status":"deleted","revoke_tesla_access_url"?}` — deletes all data |

**Sign in with Tesla (app flow):** call `authorize`, open `authorize_url` in
`ASWebAuthenticationSession` with callback scheme `milesight`; Tesla redirects to the server,
which bounces to `milesight://auth/callback?code&state`; POST those to `callback`.

```json
// Session
{"access_token":"eyJ…","refresh_token":"Wq…","token_type":"Bearer","expires_in":900,
 "expires_at":"2026-10-14T06:23:55Z","user":{…User}}
// User
{"id":"uuid","email":"driver@example.com","plan":"free","transport":"sim","created_at":"…",
 "preferences":{"distance_unit":"mi","temperature_unit":"f","currency":"USD",
   "energy_rate_per_kwh":0.15,"fast_charge_rate_per_kwh":0.45,"time_zone":"UTC"}}
```
Preference rules: `distance_unit` km|mi, `temperature_unit` c|f, `currency` ISO 4217,
rates 0–10 per kWh, `time_zone` IANA name.

## Vehicles & live state
| Method | Path | Notes |
|---|---|---|
| GET | `/v1/vehicles` | `{"vehicles":[Vehicle]}` incl. `latest` snapshot |
| POST | `/v1/vehicles/sync` | re-list from Tesla (new cars) → `{"vehicles":[…]}` |
| GET | `/v1/vehicles/{id}` | `Vehicle` |
| PATCH | `/v1/vehicles/{id}` | `{"display_name"}` (owner, 1–40 chars) |
| POST | `/v1/vehicles/{id}/wake` | **idem**; owner; `{"state":"online","woke_up":bool}` |
| GET | `/v1/vehicles/{id}/live` | `LiveState`; marks the car "watched" (faster polling) |
| GET | `/v1/vehicles/{id}/ws` | WebSocket, see below |

```json
// Vehicle
{"id":"uuid","vin":"5YJ3E1EA…","display_name":"Midnight","model":"Model 3","trim":"Long Range AWD",
 "color":"MidnightSilver","region":"na","role":"owner","state":"online","sync_status":"polling",
 "last_seen":"…","latest":{…LiveState}}
// LiveState (sub-objects omitted when unknown; charging only while plugged in)
{"vehicle_id":"uuid","state":"online","updated_at":"…","odometer_km":19868.4,
 "battery":{"percent":64,"usable_percent":63,"range_km":324.2,"ideal_range_km":337.1,"est_range_km":290.0,
   "charge_limit_pct":80,"charge_state":"charging","plugged_in":true,"charge_port_open":true},
 "charging":{"power_kw":11,"amps":48,"amps_request":48,"amps_max":48,"voltage":240,"phases":1,
   "energy_added_kwh":12.3,"range_added_km":79.7,"rate_kph":55.0,"minutes_to_limit":95,
   "fast_charger":false,"fast_charger_type":"…"},
 "climate":{"on":false,"inside_temp_c":21.5,"outside_temp_c":12,"driver_set_c":21,"passenger_set_c":21,
   "preconditioning":false,"defrost_on":false,"fan_status":0},
 "drive":{"shift_state":"D","speed_kph":72.4,"power_kw":18.2},
 "location":{"lat":37.39,"lon":-122.15,"heading_deg":271,"place":"Home"},
 "closures":{"locked":true,"doors_open":false,"windows_open":false,"frunk_open":false,"trunk_open":false,
   "sentry_mode":false,"user_present":false},
 "software":{"version":"2025.38.6","update_available":true,"update_version":"2025.44.2","update_status":"available"}}
```
Enumerations: `state` online|asleep|offline|unknown · `sync_status`
polling|idle|suspended|error|streaming · `charge_state`
disconnected|no_power|starting|charging|stopped|complete · `shift_state` P|R|N|D|"".

### WebSocket `/v1/vehicles/{id}/ws`
Auth with the `Authorization` header (native) or `?access_token=` (browsers). Every message:
`{"type": "...", "vehicle_id": "...", "at": "...", "data": {...}}`.
| type | data |
|---|---|
| `state` | `LiveState` (first message is always the current state) |
| `trip` | `Trip` (in progress or just ended) |
| `charge` | `ChargeSession` (in progress or just ended) |
| `event` | `Event` (notification) |
| `command` | `CommandResult` (from any client) |

Client may send `{"type":"refresh"}` to request an immediate read. Server pings every 25 s.
A close code `1013` (try again later) means the client fell behind: reconnect.

## Commands — `POST /v1/vehicles/{id}/commands/{name}` (**idem**, owner)
Sleeping cars are woken first (`woke_up: true`). Rate limit: 12/min/vehicle.
| name | body |
|---|---|
| `climate` | `{"on":true,"temp_c":21.5}` (temp optional, 15–28, rounded to 0.5) / `{"on":false}` |
| `lock`, `unlock`, `honk`, `flash_lights`, `frunk`, `trunk` | `{}` |
| `charge_port`, `charge_port_close` | `{}` |
| `charging` | `{"action":"start"\|"stop"}` · `{"action":"set_amps","amps":1–48}` · `{"action":"set_limit","limit_pct":50–100}` |
| `sentry` | `{"on":bool}` |
| `windows` | `{"action":"vent"\|"close"}` |

```json
// CommandResult (200 ok, or 409 when the car refused)
{"request_id":"uuid","command":"climate","status":"ok","reason":"…","woke_up":true,"at":"…"}
```

## History & analytics
| Method | Path | Query | Returns |
|---|---|---|---|
| GET | `/v1/vehicles/{id}/telemetry` | `from,to` (default last 24 h), `resolution` raw\|1m\|5m\|15m\|1h\|6h\|1d (auto if omitted; raw ≤ 7 days), `limit` ≤ 20000 | `{"from","to","resolution","points":[TelemetryPoint]}` |
| GET | `/v1/vehicles/{id}/trips` | `from,to,limit,cursor` | `Page<Trip>` newest first |
| GET | `/v1/vehicles/{id}/trips/{trip_id}` | — | `TripDetail` = Trip + `route[]` (≤ 1000 pts) |
| GET | `/v1/vehicles/{id}/charges` | `from,to,limit,cursor` | `Page<ChargeSession>` |
| GET | `/v1/vehicles/{id}/charges/{session_id}` | — | `ChargeDetail` = session + `curve[]` |
| GET | `/v1/vehicles/{id}/stats/{daily\|weekly\|monthly}` | `from,to,tz` (defaults: 30 days / 12 weeks / 12 months; user tz) | `StatsResponse` |
| GET | `/v1/vehicles/{id}/battery/health` | — | `BatteryHealth` |
| GET | `/v1/vehicles/{id}/export` | `kind=trips\|charges`, `from,to` | `text/csv` attachment |

```json
// Trip (ended_at absent while driving)
{"id":"uuid","vehicle_id":"uuid","started_at":"…","ended_at":"…","start_odometer_km":1000,"end_odometer_km":1012,
 "distance_km":12,"duration_s":840,"avg_speed_kph":51.4,"max_speed_kph":90,"start_battery_pct":80,"end_battery_pct":77,
 "start_range_km":400,"end_range_km":386,"energy_used_kwh":1.96,"efficiency_wh_km":163.3,
 "start_location":{"lat":…,"lon":…},"end_location":{…},"start_place":"Home","end_place":"","outside_temp_c":14.2}
// RoutePoint: {"time","lat","lon","speed_kph","power_kw","battery_pct"}
// ChargeSession (ended_at absent while charging)
{"id":"uuid","vehicle_id":"uuid","started_at":"…","ended_at":"…","duration_s":4560,"start_battery_pct":61,"end_battery_pct":80,
 "start_range_km":…,"end_range_km":…,"energy_added_kwh":14.46,"max_power_kw":12,"avg_power_kw":11.6,"fast_charger":false,
 "charger_type":"ac","location":{…},"place":"Home","cost":2.17,"currency":"USD","end_reason":"complete"}
// ChargePoint: {"time","battery_pct","power_kw","voltage","amps","energy_added_kwh"}
// StatsResponse
{"period":"weekly","time_zone":"America/Chicago","currency":"USD","from":"…","to":"…",
 "buckets":[StatsBucket],"totals":StatsBucket}
// StatsBucket
{"start":"…","end":"…","distance_km":533,"drive_time_s":…,"trips":38,"energy_used_kwh":93.6,"efficiency_wh_km":175.6,
 "energy_added_kwh":…,"charges":7,"charging_cost":15.98,"drain_km":…,"drain_kwh":…,"idle_hours":…}
// BatteryHealth
{"nominal_capacity_kwh":75,"estimated_capacity_kwh":74.4,"nominal_full_range_km":534,"full_range_km":534,
 "degradation_pct":0,"samples":7,"history":[{"month":"2026-09-01T00:00:00Z","capacity_kwh":74.6,"full_range_km":533.1,"samples":4}]}
```
`end_reason`: complete|stopped|unplugged|asleep. Charging cost uses the place's
`energy_rate_per_kwh`, else the user's fast-charge rate (DC) or home rate (AC).

## Places, rules, push, events, billing
| Method | Path | Body / query | Returns |
|---|---|---|---|
| GET | `/v1/geofences` | — | `{"geofences":[Geofence]}` |
| POST | `/v1/geofences` | **idem** `{"name","lat","lon","radius_m"?=150,"energy_rate_per_kwh"?}` | `201 Geofence` |
| PATCH | `/v1/geofences/{id}` | partial + `"clear_energy_rate":true` | `Geofence` |
| DELETE | `/v1/geofences/{id}` | — | `204` |
| GET | `/v1/rules` | — | `{"rules":[Rule]}` (all types, defaults merged) |
| PUT (or POST) | `/v1/rules` | `{"rules":[Rule…]}` (subset ok) | `{"rules":[…merged]}` |
| GET | `/v1/push/devices` | — | `{"devices":[PushDevice]}` |
| POST | `/v1/push/devices` | `{"token":"<hex>","environment":"sandbox"\|"production","platform":"ios"}` | `PushDevice` |
| DELETE | `/v1/push/devices/{token}` | — | `204` |
| GET | `/v1/events` | `vehicle_id?, limit ≤ 100, cursor` | `Page<Event>` newest first |
| GET | `/v1/billing/plan` | — | `{"plan":"free","status":"active","store":"none"}` |

Geofence limits: name 1–60, radius 25–5000 m, 50 per account.

```json
// Rule — types and defaults
[{"type":"charge_complete","enabled":true},{"type":"charging_started","enabled":false},
 {"type":"charging_stopped","enabled":true},{"type":"low_battery","enabled":true,"params":{"threshold_pct":20}},
 {"type":"left_unlocked","enabled":true,"params":{"minutes":10}},{"type":"left_open","enabled":true,"params":{"minutes":10}},
 {"type":"software_update","enabled":true},{"type":"geofence_arrive","enabled":false},
 {"type":"geofence_depart","enabled":false},{"type":"trip_complete","enabled":false}]
// Event
{"id":42,"vehicle_id":"uuid","type":"charge_complete","title":"Midnight finished charging",
 "body":"Charged to 80% · 21.0 kWh added · USD 3.20","occurred_at":"…",
 "payload":{"battery_pct":80,"energy_kwh":21,"charge_id":"uuid","deep_link":"milesight://vehicle/<id>/charges/<cid>"},
 "delivered_at":"…"}
```
Rule params bounds: `threshold_pct` 5–50, `minutes` 1–120.

### Push payload (APNs)
`aps.alert.{title,body}`, `aps.thread-id` = vehicle id, `aps.category` = event type, plus
top-level `type`, `vehicle_id`, `event_id`, `deep_link`.

### Deep links
`milesight://vehicle/{id}` · `…/charging` · `…/charges/{cid}` · `…/trips/{tid}`.

## Versioning
`/v1` changes are additive only (new fields, endpoints, enum values — clients must ignore
unknown fields and tolerate unknown enum values). Breaking changes go to `/v2`.
