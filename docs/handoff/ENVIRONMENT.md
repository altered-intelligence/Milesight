# Environment: build, test, run

## Requirements
- Go 1.22+ (module targets 1.22; developed on 1.24).
- PostgreSQL 14+ for the Postgres store (TimescaleDB optional; migrations detect it).
- macOS + Xcode 15+ for iOS; `brew install xcodegen` to generate the project.

## Backend
```sh
cd backend
go vet ./... && go test ./...                      # unit + conformance (memory store)
MILESIGHT_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable \
  go test ./internal/store/postgres/                # Postgres conformance (creates/drops temp DBs)
go run ./cmd/server                                 # simulator + in-memory store, no setup needed
```
Postgres with Docker: `docker compose up -d db` then set `DATABASE_URL` (see `.env.example`).

## Running a local Postgres without Docker (Linux sandbox)
Postgres refuses to run as root and needs a data dir the `postgres`-running user can reach:
```sh
mkdir -p /tmp/milesight-pg && chown <user> /tmp/milesight-pg
su <user> -c "/usr/lib/postgresql/16/bin/initdb -D /tmp/milesight-pg/data -U postgres --auth=trust"
su <user> -c "/usr/lib/postgresql/16/bin/pg_ctl -D /tmp/milesight-pg/data -o '-p 55432 -k /tmp/milesight-pg -c listen_addresses=127.0.0.1' -l /tmp/milesight-pg/log.txt start"
```

## Sandbox quirk: Go module downloads (S01 workspace)
The S01 sandbox blocked `proxy.golang.org`, `gopkg.in` and `golang.org/x` vanity URLs but
allowed GitHub. Modules were fetched with `GOPROXY=direct`, and a local file proxy was built
from GitHub mirrors (`golang/crypto`, `golang/text`, `golang/sync`, `go-yaml/yaml`,
`go-check/check`). Hashes were checked against pgx's own `go.sum` and match the official
checksum database, so `go.sum` in the repo is correct for normal environments.
On a normal machine none of this is needed.

## iOS
```sh
cd ios
xcodegen generate            # regenerates Milesight.xcodeproj from project.yml
open Milesight.xcodeproj
```
Swift packages can be checked individually: `cd ios/Packages/CoreKit && swift test`.
The app talks to `http://localhost:8080` by default in Debug (configurable in Settings).

## CI
`.github/workflows/ci.yml` — backend job (gofmt, vet, `go test -race` against a TimescaleDB
service with `MILESIGHT_EXPECT_TIMESCALE=1`, fixture generator, server + Docker build) and iOS
job on `macos-15` (`swift test` per kit, `xcodegen generate`, unsigned simulator build).
If job logs can't be downloaded from your environment, read step outcomes with:
`gh api repos/altered-intelligence/Milesight/actions/jobs/<job-id> --jq '.steps[] | "\(.name) \(.conclusion)"'`.

## Regenerating client fixtures
After any API change: `cd backend && go run ./cmd/fixtures -out ../ios/Packages/CoreKit/Tests/CoreKitTests/Fixtures`
then update Swift models until `swift test` in `ios/Packages/CoreKit` passes.
