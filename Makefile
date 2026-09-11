.PHONY: build check run setup smoke

build:
	mkdir -p bin
	go build -o bin/pagelode ./cmd/pagelode

check:
	go test ./...
	go vet ./...
	go test -race ./...
	cd browser && bun run check
	cd browser && bun test

run:
	go run ./cmd/pagelode serve

setup:
	go mod download
	cd browser && bun install --frozen-lockfile
	cd browser && bunx patchright install chromium

smoke:
	./scripts/smoke.sh
