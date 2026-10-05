# PageLode roadmap

## Direction and current status

Expand PageLode into a general scraping service with the workflow: authenticate, explore pages, discover data endpoints, extract records, and run repeatable collections.

Implemented today:

- Markdown extraction through `POST /extract` and the CLI.
- Per-page network discovery through `POST /discover`, including HAR analysis.
- Printing Press endpoint classification and grouping.
- Named authenticated Patchright profiles, including session-cookie persistence.
- Compact discovery reports with optional verbose request evidence.
- HTTP, chromedp, and Patchright loading with shared limits and escalation.

All features below are planned. Proposed routes and commands are not available yet. Keep the existing Markdown `/extract` and network-analysis `/discover` contracts. Go owns orchestration, analysis, limits, jobs, and reports; Patchright owns browser interactions. External model and proxy credentials remain caller-owned.

## Priorities

| Priority | Feature | Proposed interface | Outcome |
|---|---|---|---|
| Highest | Interactive authenticated browser sessions | `/sessions`, `pagelode session` | An agent can inspect and interact with a logged-in application |
| Highest | Site mapping and bounded crawling | `/map`, `/crawl` | Discover page URLs and traverse selected parts of a site |
| Highest | Authenticated endpoint scans | `/scan`, `pagelode scan` | One endpoint inventory across pages and browser interactions |
| Highest | Structured record extraction | `/scrape`, `pagelode scrape` | Return records with named, validated fields |
| Highest | Collection from discovered APIs | `/collect`, `pagelode collect` | Execute a reusable endpoint recipe and follow pagination |
| Implemented | Email finding | `/emails`, `pagelode emails` | Find published email addresses quickly and retain their source pages |
| High | Batch operations and background jobs | `/batch`, `/jobs/{id}` | Stream results, track progress, cancel, and resume work |
| High | Configurable rendering and dynamic content | Fetch, scrape, and session options | Load content beyond the initial page snapshot |
| Medium | Additional outputs and document processing | `/fetch`, `/transform`, `/parse` | HTML, text, metadata, screenshots, PDFs, and parsed documents |
| Medium | Caching, incremental refresh, and monitoring | Cache options and `/monitors` | Avoid repeat work and detect content or schema changes |
| Later | Search and agent/data integrations | `/search`, MCP, SDKs, connectors | Discover sources and deliver records to other tools |

## Interactive sessions and authenticated scans

Open a persistent Patchright session from a named profile. Return an explicit session ID for subsequent inspection, navigation, click, fill, scroll, wait, report, and close commands. Inspection provides a compact page description and element references suitable for agents.

Capture network traffic before the first navigation and throughout the session. Attribute observations to pages and interactions; aggregate endpoints using the existing classifier and grouping. Keep request evidence behind verbosity controls and credentials private.

Use the same operations for automatic scans. Bound hosts, pages, depth, duration, requests, and retained bodies. Explore internal links and navigation controls. Form submission and account-changing actions remain explicit operations. Enforce exclusive profile use, idle expiry, cancellation, and detection of expired authentication.

**Done when:** a local authenticated application can be explored manually by an agent and automatically by a scan, including a tab that loads an extra API. Both produce the expected combined endpoint inventory and release the profile afterward.

## Site mapping and crawling

Mapping discovers page URLs; network discovery identifies API calls. Keep those outputs distinct. Seed maps from sitemaps, sitemap indexes, and page links. Support host/subdomain scope, include/exclude path patterns, query handling, canonicalization, and URL deduplication.

Crawling retrieves the mapped or discovered pages with bounded depth and page counts. Add per-host concurrency, rate limits, robots policy, retry delays, and throttling backoff. Report skipped and failed URLs alongside completed pages.

**Done when:** a fixture with sitemap entries, duplicate URLs, redirects, excluded routes, and multiple depths yields a stable map and a crawl that obeys every configured bound.

## Structured record extraction

Support repeating-item CSS selectors, XPath where useful, named field selectors, attributes, relative URL resolution, tables, and structured JSON-LD. Add JSON paths for records contained in API responses. Validate records against a supplied schema and report missing fields and type failures.

Offer optional schema-guided model extraction with caller-supplied model configuration after deterministic extraction works. Return structured records separately from the existing Markdown output.

**Done when:** article, product, listing, table, and JSON fixtures return stable records with validated fields, including rendered pages and malformed records.

## Email finding

**Status: implemented.** `pagelode emails` and `POST /emails` crawl contact and directory pages, extract published addresses from full HTML and captured JSON, decode common obfuscation, deduplicate addresses, and retain source URLs. Page, address, and duration limits bound each search. Named profiles use serial Patchright captures.

See [email finding documentation](emails.md) for usage, response fields, rendering controls, and fixed crawl limits. Fixture tests cover prioritization, sitemaps, footer addresses, encoded and multiple mailto recipients, structured data, obfuscation, duplicates, crawl bounds, failed pages, query redaction, and profile routing. The smoke test checks JavaScript-loaded contact JSON through Chromedp and Patchright.

Remaining improvements: explicit path filters, configurable crawl depth and browser readiness, shared page caching, per-host scheduling, and JSONL/CSV export.

## Endpoint collection recipes

Save an observed endpoint as a reusable recipe with method, parameter bindings, record location, field mappings, session requirements, and pagination. Keep secrets outside recipe files. Verify replay behavior before choosing direct HTTP; use the authenticated browser when browser state is required.

Handle page, offset, cursor, and next-link continuation, repeated cursors, empty results, record deduplication, and collection limits. Return continuation state for interrupted collections.

**Done when:** the same fixture records can be collected through DOM extraction and its discovered API, including pagination and resume, without duplicate records.

## Batch operations and background jobs

Accept explicit URL batches and long-running crawl, scan, and collection jobs. Return a job ID, progress counts, partial results, errors, and final status. Provide cancellation, paginated result retrieval, and NDJSON or equivalent incremental delivery. Add optional completion webhooks.

Persist checkpoints and configuration so interrupted work can resume. Define result retention and cleanup. Keep job behavior shared between CLI and HTTP.

**Done when:** a multi-page fixture streams completed results, exposes progress and failures, cancels cleanly, and resumes from a checkpoint without repeating completed work.

## Rendering controls and dynamic content

Expose readiness controls such as waiting for a selector or bounded condition, rather than relying only on a fixed delay. Support bounded browser actions, resource blocking, viewport and locale configuration, frame inspection, and Shadow DOM access where needed.

Handle lazy loading, load-more controls, infinite scroll, and virtualized lists. Accumulate records or network responses during scrolling so items removed from the DOM remain available. Return explicit coverage limits when scrolling stops.

**Done when:** fixtures requiring delayed rendering, a load-more click, a frame, and a virtualized list produce complete records within configured limits.

## Outputs and document processing

Expose raw HTML, cleaned HTML, plain text, metadata, screenshots, and PDF export through explicit output options. Add offline HTML transformation so previously fetched content can be processed without another request. Support PDF and document input parsing with separate size and processing limits; evaluate OCR after text-based parsing.

**Done when:** the same page yields consistent HTML, text, Markdown, metadata, and visual outputs, and local document fixtures can be parsed without a browser fetch.

## Caching and refresh

Add cache freshness and bypass controls. Partition authenticated cache entries by profile so private content cannot be reused across identities. Store snapshots and record IDs for incremental refresh, content diffs, and endpoint-schema changes.

Add scheduled monitors after jobs and persistent storage exist. Report added, changed, removed, and unchanged content; optionally deliver change events through webhooks.

**Done when:** repeat requests reuse eligible cached results, different profiles remain isolated, and fixture edits produce the expected record and schema differences.

## Search and integrations

Add a caller-configured search adapter that can return source URLs and optionally scrape selected results. Build MCP tools and SDKs on stable application methods shared with the CLI and HTTP API. Provide JSONL/CSV export first, followed by optional database or object-storage connectors.

Extend proxy configuration with caller-supplied pools, geography selection, and controlled rotation where needed. Add job/browser utilization metrics and representative protected-site tests. Managed browser fleets and owned proxy networks are a separate infrastructure expansion, not a prerequisite for these features.

**Done when:** search results can feed a bounded scrape job, agent tools match CLI/API behavior, exports preserve record schemas, and configured proxy policies behave consistently.

## Recommended build order

1. Interactive sessions and authenticated endpoint scans.
2. Structured records and endpoint collection recipes.
3. Site mapping, crawling, configurable dynamic rendering, building on the implemented email-finding tool.
4. Shared batch/background jobs and resumable storage.
5. Additional outputs, document parsing, caching, and monitoring.
6. Search, MCP/SDKs, and delivery integrations.

## Comparison references

These sources informed the feature inventory; the roadmap describes PageLode's proposed implementation, not integrations with those services.

- [Spider API](https://spider.cloud/docs/api/): crawling, scraping, links, search, configurable loading, streaming, and outputs.
- [Spider browser API](https://spider.cloud/docs/api/browser/): browser sessions over CDP and interaction support.
- [Spider transform API](https://spider.cloud/docs/api/transform/): conversion of existing content without another fetch.
- [Firecrawl OpenAPI](https://docs.firecrawl.dev/api-reference/v2-openapi.json): scrape, map, crawl, batch jobs, interaction, extraction, parsing, and monitoring surfaces.
- [Firecrawl browser sessions](https://docs.firecrawl.dev/features/browser): persistent profiles and executable browser sessions.
- [Firecrawl change tracking](https://docs.firecrawl.dev/features/change-tracking): stored snapshots and content differences.
- [Crawl4AI self-hosting](https://docs.crawl4ai.com/core/self-hosting/): streaming, asynchronous jobs, webhooks, and specialized outputs.
- [Crawl4AI extraction strategies](https://docs.crawl4ai.com/extraction/no-llm-strategies/): deterministic CSS and XPath extraction.
- [Crawl4AI deep crawling](https://docs.crawl4ai.com/core/deep-crawling/): traversal strategies, filtering, and bounds.
- [Crawl4AI virtual scrolling](https://docs.crawl4ai.com/advanced/virtual-scroll/): retaining content replaced during scrolling.
