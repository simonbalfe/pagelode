# PageLode beyond Markdown extraction

## Product direction

Make PageLode a general scraping service that can collect structured records from HTML, rendered pages, and the data APIs behind websites. A caller should be able to fetch a URL, discover where its data comes from, extract records, follow pagination, and resume a collection job. Markdown remains one output of that pipeline.

Printing Press is a source of design ideas for PageLode's own implementation. The immediate priority is its automated endpoint analysis, exposed as a separate discovery input mode that returns a breakdown for each loaded page. Reusable collection recipes and incremental collection follow that capability.

Keep `POST /extract` and its current response intact while new capabilities are added. Existing routing, classification, browser escalation, deadlines, proxy settings, and attempt evidence are the shared acquisition layer.

## What to take from CLI Printing Press

| Printing Press capability | PageLode adaptation | Priority |
|---|---|---|
| HAR import and browser traffic analysis (`internal/browsersniff`) | Import a HAR or capture bounded network traffic during a render; classify document requests, JSON/GraphQL calls, and noise. Find the responses that contain the desired records. | Immediate priority |
| Endpoint descriptions, request parameters, and pagination | Save an observed request as a reusable scraping recipe; parameterize searches and page cursors; collect records directly from the endpoint when verified. | Core scraping expansion |
| Discovery provenance and `traffic-analysis.json` | Attach source URL, final URL, loader, response status, timestamp, content type, and request/response evidence to each artifact. Redact credentials before writing captures. | First |
| SQLite, FTS5, incremental sync (`store.go.tmpl`) | Store records, stable IDs, pagination checkpoints, and collection timestamps. Resume interrupted jobs and refresh collections without duplicating records. Add local search where useful. | After collection recipes |
| CLI and MCP from one client | Keep Go CLI and HTTP API backed by the same application service. Add a small MCP surface only for stable read operations such as fetch, search, and discovery. | Later |
| Verification and proof of behavior | Fixture tests for each output mode, HAR import, redaction, crawl bounds, and end-to-end search from stored pages. | Every phase |

The central workflow is: inspect a site, discover its data source, verify a collection recipe, and run that recipe through PageLode. Discovery evidence stays with the recipe so changes in the site can be diagnosed.

## Implementation sequence

### Discovery input and per-page breakdown

Use the separate `POST /discover` interface:

```json
{
  "url": "https://example.com/listings",
  "waitMs": 1500
}
```

This is implemented through `POST /discover` and `pagelode discover <URL>`. A normal extraction returns content; discovery returns a structured page analysis. Both use the same Go loading service. HAR input runs the same analyzer on an existing capture.

For discovery, browser instrumentation must be active before navigation even when direct HTTP returns usable HTML. Capture the initial document, redirects, XHR/fetch responses, relevant resource metadata, and request initiators where available. Attribute each request to the page and frame that caused it. Each page in a multi-page job gets its own breakdown; an aggregated endpoint index may deduplicate shared endpoints while retaining those page references.

Adapt the following analysis from Printing Press's `internal/browsersniff`:

| Analysis | Breakdown output |
|---|---|
| API/noise classification | Data requests, HTML documents, assets, analytics, and exclusion reasons |
| Endpoint normalization and grouping | Host, method, observed URLs, normalized path, call count, and statuses |
| Request schema inference | Path/query parameters, JSON/form fields, observed types, and sanitized examples |
| Response schema inference | Object/array structure, nested fields, observed types, and sample counts |
| Protocol detection | REST/JSON, GraphQL operations and persisted-query signals, RPC/batch envelopes, and embedded page data; flag unsupported transports |
| Authentication analysis | Observed bearer/API-key/cookie/CSRF signals and domain scope, with credential values removed |
| Protection and reachability analysis | Challenge/throttling evidence and browser/session requirements suggested by observations |
| Request sequence analysis | Request order and candidate dependencies, with evidence for each inference |
| Pagination detection | Page/offset/limit/cursor parameters, next links, and response continuation fields |
| Candidate operation analysis | Likely list/detail/search operations and useful data sources for scraping |

The response contains `page`, `summary`, `endpoints`, `protocols`, `auth`, `protections`, `pagination`, `dataSources`, and `warnings`. Verbose output additionally includes request evidence, evidence references, `sequences`, and loader `attempts`. Inferred signals carry an inference label. Observing credentials or a successful browser response does not establish that a request can be replayed directly; report replayability as untested until separately verified.

Record capture duration, entry counts, body limits, truncation, and capture failures so callers can assess coverage. Support a bounded initial-load observation window first, followed by optional explicit scroll/click/wait steps for data loaded after interaction. JavaScript endpoint strings can later become unobserved candidates with a separate evidence type.

**Done when:** a local page containing REST calls, GraphQL operations, cursor pagination, analytics noise, and an authentication signal produces the expected per-page breakdown; the equivalent HAR produces matching endpoint analysis; secrets are absent from all returned evidence.

### 1. Separate acquisition from presentation

Create an internal `Fetch` result that retains bounded response bytes or rendered HTML, final URL, status, headers needed for classification, content type, session, and attempt evidence. Keep the current classifier and loader waterfall. Move Markdown conversion behind a presentation step so `POST /extract` still produces the same response.

Add `POST /fetch` with explicit `output` values: `markdown`, `text`, `html`, and `metadata`. Return a common envelope with `url`, `finalUrl`, `status`, `contentType`, `provider`, `attempts`, and the selected content. HTML must have a separate byte limit from Markdown. Treat PDF and binary downloads as later formats with their own size and parsing rules.

**Done when:** the existing `/extract` contract and smoke fixture pass; the same HTML fixture can be retrieved as HTML, text, and Markdown; loader attempts agree across formats.

### 2. Add structured page output

Expose title, canonical URL, meta description, links, headings, JSON-LD, and basic tables as structured fields. Add deterministic record extraction with a repeating-item CSS selector and named field selectors, including attribute extraction and URL resolution. Keep source references to DOM elements or JSON-LD blocks where practical.

**Done when:** fixtures cover article, product, listing, and JavaScript-rendered pages, including redirects and missing fields.

### 3. Complete discovery capture coverage

The discovery interface above is implemented; extend its capture coverage as use cases require. Start with automated chromedp capture and the common analyzer, then add HAR input and capture parity in Patchright. Keep analysis and grouping in Go; the browser adapter returns captured observations. Link response samples to the page and interaction that caused them.

**Done when:** importing a fixture HAR yields stable endpoint groups and redacted evidence; a local JavaScript fixture produces the same inventory through live capture.

### Future feature: interactive browser sessions and automatic scans

**Status: planned, not implemented.** Current discovery observes one page load. Named authenticated profiles are implemented, but interactive sessions and multi-page scans remain future work.

Keep a Patchright browser open while an agent inspects and navigates an authenticated application. Go owns session IDs, commands, limits, endpoint matching, and report assembly. The browser worker handles DOM inspection, navigation, clicks, scrolling, form input, and network capture. A session selects an existing named profile, preserving its authenticated browser state.

Proposed CLI surface:

```sh
pagelode session open --profile account https://app.example.com
# The open command returns a session ID used by subsequent commands.
pagelode session inspect <session-id>
pagelode session click <session-id> <element-id>
pagelode session navigate <session-id> <URL>
pagelode session scroll <session-id>
pagelode session report <session-id>
pagelode session close <session-id>

pagelode scan --profile account --max-pages 20 https://app.example.com
```

`inspect` returns a compact page description and stable references to links, buttons, and form controls. Explicit interaction commands let an agent explore tabs, menus, and authenticated routes. Provide form input and submission as separate explicit actions when required; automatic scanning follows internal links and navigation controls without submitting forms or invoking account-changing actions.

Network capture starts before the first navigation and continues across interactions. Each observation belongs to a page and interaction. Go applies the existing Printing Press classifier and endpoint grouping across the entire session, preserving operation metadata and cumulative call counts. The default report stays compact; `--verbose` includes request evidence and interaction references. Raw credentials and browser storage remain private.

`scan` uses the same session operations to explore the application automatically. Bound allowed hosts, pages, navigation depth, duration, retained requests, and response bytes. Deduplicate visited URLs and endpoint groups, handle throttling, and return partial results when a limit is reached. Browser navigation controls and links discovered after rendering extend coverage beyond a static link crawl.

Give sessions explicit close and idle-expiry behavior, propagate cancellation, and release the persistent profile when the session ends. Prevent simultaneous browser sessions from opening the same profile. A scan must report loss of authentication rather than treating login pages as authenticated coverage. Share these operations between CLI and HTTP; consider MCP after the interface is stable.

**Done when:** an agent can open a saved authenticated profile, inspect a local test application, navigate through several routes, click a tab that loads an additional API, and retrieve one combined endpoint report. Automatic scanning should find the same read endpoints within its limits. Tests must cover profile isolation, expiry, cancellation, duplicate routes, capture limits, credential redaction, and exclusion of automatic form submissions.

### 4. Turn discoveries into collection recipes

A recipe defines the source URL or observed endpoint, allowed request parameters, session requirements, record location, field mappings, and pagination strategy. Support CSS selectors for DOM records and JSON paths for API responses. Start with explicit page/offset/cursor/next-link rules; treat inferred pagination as a candidate that must be verified against captured samples.

Use direct HTTP for verified replayable endpoints. Browser-dependent requests retain a browser execution path with coherent cookies, user agent, and proxy. Keep credentials outside portable recipe files. Bound browser steps such as waiting for a selector, clicking a pagination control, or scrolling; return continuation state when more work is required.

**Done when:** a local listing fixture can be collected through both DOM extraction and a discovered JSON endpoint, with identical record IDs and no duplicate pages.

### 5. Crawl and refresh collections

Add a bounded crawl or recipe collection job with allowed hosts, maximum pages/records, depth, duration, and per-host rate limits. Respect retry delays and reduce request rate after throttling. Reuse canonical URL and content hashes to avoid duplicates. Store records, documents, recipe versions, and cursors in SQLite with collection timestamps and optional FTS5 search. Record schema changes and failed record validation instead of silently accepting empty collections.

**Done when:** a local site can be crawled, searched, resumed, and recrawled without revisiting unchanged content or exceeding configured bounds.

### 6. Agent access

Add CLI subcommands for `fetch`, `discover`, `scrape`, `crawl`, and `search`, with JSON, JSONL/CSV record export, and compact output. Add MCP tools only after those operations have stable request and response types. Share application methods with HTTP and CLI so behavior does not drift.

**Done when:** HTTP, CLI, and MCP paths produce equivalent results for the same fixture and honor the same limits.

## First build slice

The discovery slice is implemented with `/discover`, chromedp and Patchright capture, HAR input, and a per-page endpoint analyzer. See [discovery.md](discovery.md) for the current contract and limits. Build structured selectors, collection recipes, storage, and MCP after discovery works. This ordering makes the requested page breakdown available before expanding the surrounding scraping workflow.
