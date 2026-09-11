# PageLode

PageLode is the Go-first successor experiment for OpenExtract.

## Architecture

- Keep orchestration, classification, extraction, limits, retries, domain memory, and the public API in Go.
- Keep the Patchright adapter small and browser-specific. It must not contain target routing or extraction policy.
- Route JavaScript shells to Rod and confirmed challenge pages directly to Patchright.
- Preserve the OpenExtract `/extract` response contract while the migration is evaluated.
- Do not add managed extraction providers; those remain caller-owned fallbacks.

## Validation

- Run `go test ./...`, `go vet ./...`, and `go test -race ./...` after Go changes.
- Run `bun run check` and `bun test` under `browser/` after TypeScript changes.
- Run `make smoke` after cross-boundary changes.

