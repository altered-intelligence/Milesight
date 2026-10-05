# Recorded Decisions (ADR-style log)

Status: Draft — confirm in review.

## ADR-001 — Backend language: Go
- **Context:** responsiveness was the top priority; need real-time streaming, high concurrency,
  low latency, and many concurrent client connections; deployable easily (static binary).
- **Decision:** Use Go for backend services (ingestion, aggregation, API).
- **Alternative:** TypeScript/Node — one typed language across backend + web; fine if type
  sharing with the web app matters more than raw concurrency.
- **Consequence:** Web client and backend drift on types unless you adopt a contract-first API
  (protobuf/OpenAPI) — addressed by the public API spec.

## ADR-002 — Commercial edge → Official Tesla Fleet API
- **Context:** the product is commercial; the legacy Owner API is not productized and is
  restricted at scale.
- **Decision:** Target Fleet API as the production transport. Owner API is dev/bootstrap only.
- **Consequence:** Requires Tesla developer account + app registration + per-owner consent;
  region-partitioned; apply early (lead time). Protect with a transport abstraction.

## ADR-003 — Transport abstraction
- **Decision:** All data/command flows sit behind a `TeslaTransport` interface with
  `FleetTransport` and `OwnerTransport` adapters. Swapping transports is config, not a rewrite.

## ADR-004 — Streaming-first realtime
- **Decision:** Prefer Fleet streaming telemetry + WebSocket push to clients over polling, for a
  reactive UX and to respect API limits. Owner-API fallback polls at a low, sleep-aware cadence.

## ADR-005 — Data storage
- **Decision:** PostgreSQL for entities + TimescaleDB for telemetry hypertables with continuous
  aggregates and compression/lifecycle policies to control cost.

## ADR-006 — Basic auth/dev path
- **Decision:** Bootstrapping uses Owner-API SSO for a personal dev vehicle; production OAuth is
  Fleet client-credentials + per-user consent. Roles/plans come with commercial billing.

## ADR-007 — Scope boundary
- **Decision:** Deliver "Tessie-like" features/UX; do NOT copy Tessie branding, logos, or
  copyrighted UI/assets. Review Tesla third-party dev terms before launch.

## ADR-008 — Product name: Milesight
- **Context:** needed an original product name that signals driving/trip analytics and
  telemetry without colliding with "Tessie" or Tesla product branding.
- **Decision:** Name the product **Milesight**. Runner-up was **Chargebook** (battery/charging
  angle) if the analytics focus shifts later.
- **Consequence:** Use "Milesight" for all branding, store listings, and code identifiers; keep
  the docs' "Tessie-like" phrasing strictly for feature comparison, not naming.
