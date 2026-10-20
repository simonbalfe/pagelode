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

- Importable packages live under `pkg/` and must not depend on `internal/`.
- Group small related code into the package that uses it instead of a new single-file package; split a package only when it grows a separate job.
- App-only wiring lives in `internal/`: CLI and env config (`internal/cli`), HTTP API (`internal/api`), and the sqlc-generated Postgres store (`internal/store`; edit `schema.sql`/`queries.sql`, then `make generate`).
- `cmd/pagelode` holds only `main`, which calls `internal/cli`.
- Promote a package from `internal/` to `pkg/` only when another project needs to import it.
- Keep one command per job; variants are flags (`emails --input`) or subcommands (`extract batch`, `map endpoints`), not new top-level commands.

## Code

- Never write code comments of any kind, including doc comments. Only `//go:` directives are allowed.
- Build the whole feature and run one end-to-end check before writing tests.

## Validation

- Run `go test ./...`, `go vet ./...`, and `go test -race ./...` after Go changes.
- Run `bun run check` under `browser-service/` after TypeScript changes.
- Run `make smoke` after cross-boundary changes.

