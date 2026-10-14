.PHONY: run build test test-pg race vet lint tidy docker

run:            ## simulator + in-memory store, no setup required
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test ./...

test-pg:        ## also run the Postgres conformance suite (needs a server you can CREATE DATABASE on)
	MILESIGHT_TEST_DATABASE_URL=$${MILESIGHT_TEST_DATABASE_URL:-postgres://milesight:milesight@localhost:5432/milesight?sslmode=disable} go test ./...

race:
	go test -race ./...

vet:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

tidy:
	go mod tidy

docker:
	docker compose up --build
