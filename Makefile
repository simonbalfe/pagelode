.PHONY: build check run run-browser setup smoke

build:
	mkdir -p bin
	go build -o bin/pagelode ./cmd/pagelode

check:
	go test ./...
	go vet ./...
	go test -race ./...
	cd browser-service && bun run check

run:
	go run ./cmd/pagelode serve

run-browser:
	cd browser-service && bun run dev

setup:
	go mod download
	cd browser-service && bun install --frozen-lockfile
	cd browser-service && bunx patchright install chromium

smoke:
	./scripts/smoke.sh
