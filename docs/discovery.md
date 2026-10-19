# Endpoint discovery

PageLode exposes two operations:

| Interface | Result |
|---|---|
| `POST /extract` or `pagelode extract <URL>` | Clean Markdown, links, and extraction attempts |
| `POST /discover` or `pagelode discover <URL>` | A structured breakdown of the requests and data sources observed during a page load |

Discovery loads the page with browser instrumentation active before navigation. Go analyzes the captured requests into endpoint descriptions. Each result belongs to one input page.

## Use the CLI

```sh
pagelode discover https://example.com/listings
pagelode discover --wait-ms 3000 https://example.com/listings
pagelode discover --har capture.har
pagelode discover --verbose https://example.com/listings
pagelode discover --stealth https://example.com/listings
```

Discovery always prints JSON. The default report omits request evidence, evidence references, sequence details, and loading attempts. Use `--verbose` or API `"verbose": true` to include those details. Verbose reports still redact credentials. `--wait-ms` is the longest the capture keeps observing after navigation: it ends earlier once the page goes quiet (200 ms without content requests or DOM changes; see [waiting for the page](browser-service.md#waiting-for-the-page)). Default 1500 milliseconds, minimum 100, maximum 10000. The configured extraction timeout bounds the whole operation.

HAR input runs the same analyzer without opening a browser. The CLI accepts files up to 4 MiB, including standard HAR metadata and base64 response bodies. An optional URL after `--har` identifies the page being analyzed.

## Use the API

```sh
curl -sS http://localhost:8083/discover \
  -H 'content-type: application/json' \
  -d '{"url":"https://example.com/listings","waitMs":1500}'
```

For offline traffic, send `{"har":{"log":{"entries":[...]}}}`. `url` is optional with HAR input; `waitMs` and `stealth` apply to live discovery. Request bodies are limited to 4 MiB.

Malformed input returns HTTP 400, a saturated queue returns 429, a request deadline returns 408, a stealth request on a server without `PAGELODE_PROXY_URL` returns 503, and a browser capture failure returns 502. A captured page that remains blocked returns a report with `outcome: "blocked"`; a missing page returns `outcome: "dead"`. Other completed analyses return `outcome: "ok"`, including pages where no API was observed.

## Read the report

| Field | Meaning |
|---|---|
| `page` | Input/final URLs, title, provider, and document status |
| `summary` | Captured, data, noise, dropped, incomplete, and truncated request counts; duration and endpoint count |
| `endpoints` | Groups of requests sharing a host, method, and normalized path; GraphQL operations are metadata |
| `evidence` (verbose) | Sanitized request URLs, status, resource classification, frame/initiator where available, and timing |
| `protocols` | Inferred REST/JSON, GraphQL, persisted queries, batch GraphQL, RPC, gRPC-Web, WebSocket, SSE, or HTML |
| `auth` | Observed authorization, cookie, CSRF, API-key, or credential-query signals, linked to endpoints |
| `protections` | Access denial, authentication, throttling, retry-delay, and browser challenge evidence |
| `sequences` (verbose) | Observed request order; it does not establish causal dependencies |
| `pagination` | Candidate query/body cursor, page, offset, limit, next-page fields, and next-link headers |
| `dataSources` | Candidate JSON arrays/objects and embedded JSON script blocks |
| `warnings` | Capture gaps and limits affecting analysis |
| `attempts` (verbose) | Browser loading and classification evidence |

Each endpoint includes its call count, observed statuses, query/path parameters, request and response field paths with observed types, auth signals, and evidence IDs when verbosity is enabled. Numeric segments normalize to `{id}`, supported UUIDs to `{uuid}`, and hexadecimal hashes of at least 32 characters to `{hash}`. Request scoring determines whether a request belongs in endpoint groups. Ordinary HTML navigation and Fetch/XHR requests without API evidence are excluded.

For example, a response containing listing records may produce:

```json
{
  "path": "/api/listings",
  "method": "GET",
  "protocol": "rest_json",
  "parameters": [
    {"path":"query.page","types":["number"]}
  ],
  "responseSchema": [
    {"path":"$.items","types":["array"]},
    {"path":"$.items[].price","types":["number"]}
  ],
  "replayability": "untested"
}
```

This is an endpoint excerpt. Verbose responses also identify evidence. Reports include candidate pagination such as `$.nextCursor`. Schema types are inferred from the captured samples; they describe observed values rather than an authoritative API schema.

## Endpoint matching

The classifier and path matcher are ported from CLI Printing Press commit `39cbc18971a10aba41bbc56a68e1710a03bb063d`, specifically `internal/browsersniff/classifier.go` and `DeduplicateTrafficEndpoints` in `analysis.go`. The scoring weights, positive threshold, default domain blocklist, asset matching, path patterns, method normalization, and host matching are preserved. Browser captures supply PageLode's portable exchange values; no Printing Press runtime dependency is required. The upstream MIT notice is retained in `licenses/cli-printing-press.txt`.

| Observation | Score |
|---|---:|
| Response content type contains `application/json` | +2 |
| Request content type contains JSON or URL-encoded form | +1 |
| Path contains `/api/`, `/v1/`, `/v2/`, `/v3/`, `/graphql`, `/data/`, or `/youtubei/` | +1 total |
| Response body parses as JSON | +1 |
| Host matches the default blocklist, including subdomains | −3 |
| Response content type starts with image, CSS, HTML, JavaScript, or font types | −2 |
| Lowercased complete URL ends with a recognized asset suffix | −1 |

A score greater than zero identifies an API request. Scores of zero or less identify noise. A blocklist match is a score penalty, not an unconditional veto. Resource type alone does not identify an API. These rules deliberately preserve upstream behavior, including full-URL suffix matching and UUID version/variant constraints.

Endpoint groups use lowercase hostname, uppercase trimmed method, and normalized decoded path. Query values, ports, schemes, and GraphQL operation names do not split groups. Multiple GraphQL operations are listed in `operations`; `operation` is present only when a single named operation was observed. Other analysis fields remain PageLode's implementations; this port establishes classifier and endpoint grouping parity, not complete parity with Printing Press's analysis report.

`internal/discovery/testdata/printing-press-endpoints.json` records expectations generated by running the original upstream classifier and grouping functions in an isolated temporary program. Its 337 cases cover content-type combinations, valid/invalid JSON, every default blocklist domain, domain boundaries, IDs, UUIDs, hashes, ports, method case, encoded paths, and the upstream sample capture with a placeholder credential. Tests compare individual scores, classifications, normalized paths, and all 18 resulting groups.

## Authenticated profiles

```sh
pagelode profile login creatorcrawl https://creatorcrawl.com
pagelode discover --profile creatorcrawl creatorcrawl.com
pagelode discover --profile creatorcrawl --verbose creatorcrawl.com
```

The login command opens a visible browser session on the browser service host at the URL, with a one-hour limit, and prints its CDP URL. Sign in manually, including any MFA, then press Enter in the terminal; PageLode closes the session and the browser service saves the profile. The profile saves cookies, including session cookies, local storage, and the browser's other persistent state. Session storage is tied to individual tabs and is not guaranteed to survive reopening. No credentials need to be passed to PageLode.

Go validates profile names and passes the name to the browser service, which owns profile storage. Named discovery launches the profile's browser on the service, using its `BROWSER_SERVICE_HEADLESS` setting, restores the saved cookies, and closes the browser afterward so that state is saved and the profile is released. A missing profile fails (`profile_not_found`) with a login instruction rather than silently running unauthenticated. Each profile has its own browser directory; normal discovery does not select a named profile. The browser service runs one render at a time per profile and rejects a profile held by an open login session (`profile_in_use`), so finish logging in before discovering.

API callers select an existing profile with `{"url":"https://creatorcrawl.com","profile":"creatorcrawl","verbose":false}`. Profiles belong to the browser service; every PageLode instance pointed at the same `PAGELODE_BROWSER_URL` sees the same profiles. Profiles apply to live discovery only, not HAR input.

The browser service stores profiles under `BROWSER_SERVICE_PROFILES_DIR`. It defaults to `pagelode/profiles` under the operating system's user configuration directory (on macOS, `~/Library/Application Support/pagelode/profiles`); the container image uses `/data/profiles`. Names accept 1–64 letters, numbers, underscores, or hyphens, starting with a letter or number. Profile directories are restricted to the owning user. Discovery reports never include raw cookies, storage values, or header values, including with verbosity enabled. If login expires, run the login command again for the same name.

## Loading and limits

Discovery loads the page once, in the browser service (provider `patchright`), because even a usable HTML page can make useful data requests. There is no escalation and no route memory: capturing a normal page does not teach extraction to skip its cheap HTTP attempt. The capture uses the existing proxy configuration and classification, and the browser service's own queue limits concurrency.

| Capture result | Report |
|---|---|
| Usable | `outcome: ok` |
| Blocked or retryable, with traffic captured | `outcome: blocked` with the warning "page remained blocked in the browser" |
| Blocked, no traffic | Error |
| Dead (404 or 410) | `outcome: dead` |

Stealth discovery (`--stealth` or API `"stealth": true`) only refuses to run unless `PAGELODE_PROXY_URL` is set, so the page is never loaded from the server's own address. Every capture already uses the browser service.

Retained traffic is bounded to 200 entries, 256 KiB per body, and 2 MiB of bodies per load. Schemas inspect at most eight nested levels, twenty array samples, and 512 fields per schema. The report records dropped or truncated observations. Stream and binary protocol payloads are identified by their transport signals rather than decoded into record schemas. Discovery observes initial loading and the configured wait period; explicit clicks, scrolling, and endpoint replay are future additions.

Header values and raw request/response bodies are private capture inputs. Public reports include field names and types, auth signal kinds, and evidence references. Query values are replaced with `[redacted]`; URL credentials and fragments are removed. Body values, including cursor values, are not returned. Page titles and ordinary path segments remain useful page metadata. Schema and parameter names are bounded and normalized.

## Modules

| Module | Responsibility |
|---|---|
| `internal/page/capture.go` | Portable browser traffic values and capture limits |
| `browser-service/src/render/capture.ts` | Patchright network collection |
| `browser-service/src/profiles/` | Profile directories, saved cookies (`store.ts`), and per-profile locks (`locks.ts`) |
| `internal/browser/client.go` | Render and capture requests to the browser service over HTTP |
| `internal/discovery/service.go` | Input validation, the single browser capture, and outcome mapping |
| `internal/discovery/classifier.go` | Ported Printing Press scoring and path matching |
| `internal/discovery/view.go` | Report verbosity |
| `internal/profile/store.go` | Named profile name validation |
| `cmd/pagelode/profile.go` | Interactive profile login |
| `internal/discovery/analyze.go` | Endpoint grouping, public evidence, and coverage reporting |
| `internal/discovery/schema.go` | Observed JSON/form/query shapes |
| `internal/discovery/signals.go` | Protocol, auth, pagination, and protection analysis |
| `internal/discovery/har.go` | Offline HAR normalization |
| `internal/api/discover.go` | HTTP interface |
| `cmd/pagelode/discover.go` | CLI interface |

## Verification

```sh
make check
make smoke
PAGELODE_BROWSER_TESTS=1 go test -race ./internal/browser ./internal/emails
```

The browser tests require Bun and the browser service dependencies (`bun install` and `bunx patchright install chromium` in `browser-service/`, or `make setup`). `internal/browser/browsertest` starts the real browser service with `bun`, headless on bundled Chromium. The tests load local REST/GraphQL fixtures and exercise real Patchright renders and captures through the service. Unit tests cover grouping, mixed response types, pagination, redaction, HAR decoding, input validation, and fallback routing.

## Planned interactive discovery

Persistent agent-controlled browser sessions and bounded automatic scans are future features. They will let an agent inspect pages, navigate authenticated routes, click navigation controls, and gather one endpoint report across those interactions. See the [generalization plan](generalization-plan.md#future-feature-interactive-browser-sessions-and-automatic-scans) for the proposed commands and acceptance criteria. The current CLI does not implement `session` or `scan`. The browser service already exposes raw CDP browser sessions (`POST /v1/sessions`), which `profile login` uses; see [browser service](browser-service.md).
