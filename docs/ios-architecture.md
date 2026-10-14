# iOS app architecture (SwiftUI, iOS 17+)

Status: implemented in S01; kits unit-tested and app compiled in CI (macOS). UI flows have
not yet been exercised in a simulator or on device — see `docs/handoff/LEDGER.md` (IOS-*).

## Modules
| Module | Responsibility | Depends on |
|---|---|---|
| `CoreKit` | Contract models (`Models.swift`), JSON coding (snake_case, RFC 3339 w/ nanoseconds), typed `Endpoint`s, `APIClient` actor (auth, coalesced single-use refresh, error mapping), `KeychainTokenStore`, `LiveSocket` (WebSocket), `UnitFormatter` | Foundation |
| `LiveKit` | `VehicleStore` (`@Observable`, main actor): vehicles, live state, active trip/charge, streaming with reconnect backoff, optimistic overlays | CoreKit |
| `CommandsKit` | `VehicleCommand` (typed commands + optimistic effects), `CommandCenter` (send, in-flight tracking, rollback) | CoreKit, LiveKit |
| `AnalyticsKit` | Chart series in display units (`StatsMetric`, `Curves`, `Summaries`) — no UI imports | CoreKit |
| `NotificationsKit` | `DeepLink` parsing (URLs, push payloads, OAuth callback), `PushRegistrar` | CoreKit |
| `App` (target) | SwiftUI screens, `AppModel` (session, server, navigation), push via `AppDelegate` | all kits |

Kits avoid UIKit/SwiftUI so they build and test with `swift test` on macOS.

## Data flow
1. `AppModel.start()` loads `/v1/config` (which sign-in methods exist) and restores the
   Keychain session.
2. Sign-in (`/v1/auth/dev/login`, Tesla OAuth via `WebAuthenticationSession`, or Owner token)
   returns a `Session`; `APIClient` stores it and refreshes it transparently. A failed refresh
   emits `nil` on `sessionChanges()` → app returns to sign-in.
3. `VehicleStore.refresh()` loads vehicles (each with its `latest` snapshot). The selected car
   is streamed over `/v1/vehicles/{id}/ws` while the app is in the foreground
   (`AppModel.syncStreaming`); the first message is always the current state.
4. Commands go through `CommandCenter.send`: an optimistic overlay is applied immediately and
   held for 20 s against stale reads; on failure it is removed and the previous state restored;
   on success the server's streamed state takes over.
5. History screens page through `trips`/`charges`/`events` with opaque cursors.

## Screens
Tabs: **Car** (dashboard: battery ring, range, status line, live drive/charge cards, update
banner, quick actions, climate, map; → Controls, Battery health; Settings sheet) · **Drives**
(list with totals, detail with route map + speed chart, CSV export) · **Charging** (live
session, list with cost totals, detail with power/SoC curves and map, CSV export) ·
**Insights** (daily/weekly/monthly charts: distance, efficiency, energy, cost, idle drain;
battery health) · **Alerts** (push permission, event feed with deep links, rules, places).

## Conventions
- All values from the API are SI; format only through `UnitFormatter` (user preferences).
- Free-form dictionaries (`Event.payload`, `Rule.params`) use snake_case keys, as on the wire.
- Deep links: `milesight://vehicle/{id}[/charging|/charges/{cid}|/trips/{tid}]`;
  OAuth returns to `milesight://auth/callback`.
- Swift 5 language mode; views with async helpers are `@MainActor`.

## Project generation
`ios/project.yml` (XcodeGen) generates the project, `Info.plist` (URL scheme, ATS local
networking for dev, remote-notification background mode, `MilesightAPIBaseURL` per
configuration) and entitlements (`aps-environment`). Generated files are not committed.

## Not yet built
WidgetKit widgets, App Intents/Shortcuts, StoreKit subscriptions, offline cache of last-known
state, app icon/asset catalog, UI tests. Tracked in the ledger.
