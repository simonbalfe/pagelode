# PageLode

PageLode is the Go-first successor experiment for OpenExtract.

## Architecture

- Keep orchestration, classification, extraction, limits, retries, domain memory, and the public API in Go.
- Keep the Patchright browser service (`browser-service/`) small and browser-specific. It must not contain target routing or extraction policy.
- Route JavaScript shells to Chromedp and confirmed challenge pages directly to Patchright.
- Preserve the OpenExtract `/extract` response contract while the migration is evaluated.
- Do not add managed extraction providers; those remain caller-owned fallbacks.
- For Google search, the replayable clearance is the browser-minted `__Secure-ENID` cookie (plus `GOOGLE_ABUSE_EXEMPTION` when `/sorry` was solved), replayed from the harvesting IP. Measurements and open questions: [browser service](docs/browser-service.md#google-which-cookie-the-replay-needs).

## Validation

- Run `go test ./...`, `go vet ./...`, and `go test -race ./...` after Go changes.
- Run `bun run check` under `browser-service/` after TypeScript changes.
- Run `make smoke` after cross-boundary changes.

