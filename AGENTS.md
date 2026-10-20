# PageLode

PageLode is the Go-first successor experiment for OpenExtract.

## Architecture

- Keep orchestration, classification, extraction, limits, retries, domain memory, and the public API in Go.
- Keep the Patchright browser service (`browser-service/`) small and browser-specific. It must not contain target routing or extraction policy.
- Route JavaScript shells and challenge pages to the browser service; route remembered Patchright and protected domains there directly, without a wasted HTTP attempt.
- Preserve the OpenExtract `/extract` response contract while the migration is evaluated.
- Do not add managed extraction providers; those remain caller-owned fallbacks.
- For Google search, the replayable clearance is the browser-minted `__Secure-ENID` cookie (plus `GOOGLE_ABUSE_EXEMPTION` when `/sorry` was solved), replayed from the harvesting IP. Measurements and open questions: [browser service](docs/browser-service.md#google-which-cookie-the-replay-needs).

## Layout

- Importable packages live at the module root and must not depend on `internal/`.
- App-only wiring lives in `internal/`: HTTP API (`internal/api`), env config (`internal/config`), and SQLite stores (`internal/state`, `internal/emailstore`).
- `cmd/pagelode` only wires packages together.
- Promote a package out of `internal/` only when another project needs to import it.

## Code

- Never write code comments of any kind, including doc comments. Only `//go:` directives are allowed.
- Build the whole feature and run one end-to-end check before writing tests.

## Validation

- Run `go test ./...`, `go vet ./...`, and `go test -race ./...` after Go changes.
- Run `bun run check` under `browser-service/` after TypeScript changes.
- Run `make smoke` after cross-boundary changes.

