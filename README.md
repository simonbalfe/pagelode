# PageLode

PageLode extracts clean Markdown, maps a site's page URLs, discovers the data endpoints behind web pages, and finds published email addresses.

Give it a URL and it returns the useful content, page title, internal links, and a record of how the page was loaded. It begins with a fast direct request and only opens a browser when the page actually needs one.

## Why PageLode?

Many web pages can be downloaded directly. Others need JavaScript, and some place a browser check in front of their content. Using a full browser for every request is slow and expensive, so PageLode uses a simple waterfall:

1. Replay a stored browser clearance for the domain, when one exists.
2. Load the page with a browser-like HTTP client.
3. If JavaScript is required or the page appears blocked, render it with Patchright in the browser service.
4. If the page is still blocked and CapSolver is configured, obtain a valid session and retry through the same proxy.
5. Remove navigation, advertising, and other clutter.
6. Return readable Markdown and useful metadata.

PageLode keeps the main service in Go. Patchright, whose browser tooling is built for the JavaScript ecosystem, runs in a separate Bun browser service (`browser-service/`) that Go calls over HTTP.

## Quick start

You need Go and Bun installed. On macOS and Windows the browser service uses Google Chrome by default, so install Chrome as well.

```sh
make setup
make build
make run-browser   # in a second terminal
./bin/pagelode example.com
```

PageLode prints the cleaned page as Markdown. The browser service listens on `http://127.0.0.1:8090`; PageLode calls it whenever a page needs a browser.

For the complete result, including the loader attempts:

```sh
./bin/pagelode --json example.com
```

## CLI

```text
pagelode [--json] <URL>
pagelode extract [--json] <URL>
pagelode discover [--wait-ms 1500] [--profile <name>] [--stealth] [--verbose] <URL>
pagelode discover --har <capture.har> [<URL>]
pagelode map [--limit 500] [--max-duration 30s] [--depth 2] [--render never] [--explore] [--include <path>] [--exclude <path>] [--subdomains] [--json] <URL>
pagelode emails [--max-pages 20] [--max-emails 100] [--max-duration 30s] [--render auto] [--profile <name>] [--verbose] <URL>
pagelode emails-bulk [--db data/emails.sqlite] [--workers 32] [--max-pages 8] [--max-emails 50] [--max-duration 20s] [--render never] <file|->
pagelode profile login <name> <URL>
pagelode serve
pagelode version
pagelode help
```

Put options before the URL. A bare domain such as `example.com` is accepted wherever a URL is. Every command reads its settings from environment variables; see [configuration](docs/configuration.md) for proxies, browser mode, CapSolver, and limits.

### Commands at a glance

| Command | Question it answers | Output | Browser service |
|---|---|---|---|
| `pagelode <URL>`, `extract` | What does this page say? | Markdown (or `--json`) | Only when the page needs it |
| `map` | Which pages does this site have, and which page types? | URL list (or `--json` with sources, templates, endpoints) | Optional: `--render`, `--explore` |
| `discover` | Where does this page's data come from? | JSON endpoint report | Always (one capture), except `--har` |
| `emails` | Which email addresses does this site publish? | One address per line | Optional: `--render` |
| `emails-bulk` | The same, for a list of sites | SQLite database | Optional: `--render` |
| `profile login` | Save a signed-in browser for later runs | Named profile | Always |
| `serve` | Run all of the above over HTTP | API on `PORT` | As needed |
| `version`, `help` | Print the version or usage | Text | No |

A typical workflow: `map` the site to find its page types and endpoints, `extract` a sample page of each type, `discover` the data endpoints behind the interesting ones, then `emails` for contacts.

### `pagelode <URL>` / `pagelode extract <URL>`

Loads one page through the waterfall and prints clean Markdown.

| Option | Default | Effect |
|---|---|---|
| `--json`, `-j` | off | Print the complete result: content, title, links, provider, and every loader attempt |

### `pagelode discover`

Captures a page's network traffic and prints a JSON report of its data endpoints.

| Option | Default | Effect |
|---|---|---|
| `--wait-ms` | `1500` | Longest wait for network activity after page load, 100–10000 ms; ends earlier when the page goes quiet |
| `--profile` | none | Use a saved signed-in browser profile |
| `--stealth` | off | Refuse to run unless `PAGELODE_PROXY_URL` is set. Every capture already uses the Patchright browser service |
| `--verbose` | off | Include request evidence, matching scores, and loading details |
| `--har` | none | Analyze a HAR file instead of opening a browser. An optional URL names the page it came from |

### `pagelode map`

Lists the page URLs of one site, one per line. It reads `robots.txt` sitemaps (including `.xml.gz`), follows sitemap indexes (up to 25 sitemaps), falls back to `/sitemap.xml` and `/sitemap_index.xml`, then crawls page links breadth-first from the start page. URLs are canonicalized (fragments and tracking parameters removed), deduplicated, and limited to the site's host and its `www` twin.

The crawl is built to find the right pages, not every page:

- **Depth.** `--depth 1` reads only the start page; the default `2` also reads the pages it links to. At most 200 pages are fetched.
- **Sampling.** Every discovered URL is listed, but only the first 20 pages of one URL shape (host, first path segment, segment count, query names) are fetched for further links, so 5,000 tag pages cost 20 fetches.
- **Duplicates.** A page whose visible text is a near-duplicate (simhash within 3 bits, at least 200 characters) of one already read adds its links to the list but is not expanded.
- **Robots.** Pages that the `User-agent: *` rules of `robots.txt` disallow are listed when linked but never fetched; `robotsSkipped` counts them.
- **Scripts.** Same-site `<script src>` files (up to 20) and inline scripts are scanned for endpoint paths such as `/api/users`, reported as `endpoints`. Static assets are ignored.
- **Templates.** `--json` groups the URLs into path templates such as `/tag/*` with a count and sample URLs, so one sample per page type can be scraped.
- **Browser.** `--render auto` loads a page in the browser service only when the HTTP result needs JavaScript (up to 25 pages); `always` renders every crawled page up to that cap. `--explore` also scrolls and clicks safe controls on the start page (up to 5 pages) and lists the URLs it reaches; it needs `--render auto` or `always`. Without a browser service these options fall back to HTTP with a warning.

| Option | Default | Effect |
|---|---|---|
| `--limit` | `500` | Maximum URLs, 1–5000 |
| `--max-duration` | `30s` | Time budget, 1s–2m |
| `--include` | all | Keep only URLs whose path matches this pattern, such as `/blog/*`; `*` matches any characters. Repeatable |
| `--exclude` | none | Drop URLs whose path matches this pattern. Wins over `--include`. Repeatable |
| `--subdomains` | off | Also include subdomains of the site |
| `--depth` | `2` | Link levels to follow from the start page, 1–5 |
| `--render` | `never` | `never`, `auto`, or `always`: when pages load in the browser service |
| `--explore` | off | Scroll and click safe controls on the start page to reveal links; needs `--render auto` or `always` |
| `--json` | off | Print the report: each URL with its `source` (`sitemap`, `page`, `link`, `rendered`, or `explore`) and `lastModified`, plus `endpoints`, `templates`, crawl counts, and warnings |

Exits `1` only when no URL was found and nothing could be loaded. `POST /map` takes `{"url", "limit", "maxDurationMs", "subdomains", "include", "exclude", "depth", "render", "explore"}` and returns the same report. `scripts/discovery-bench.sh <site>` compares a map against Katana, when installed, and prints URL counts per source.

### `pagelode emails`

Crawls a site and prints one published email address per line.

| Option | Default | Effect |
|---|---|---|
| `--max-pages` | `20` | Pages to visit, 1–100 |
| `--max-emails` | `100` | Stop after this many unique addresses, 1–1000 |
| `--max-duration` | `30s` | Crawl time budget, 1s–2m |
| `--render` | `auto` | `auto` uses a browser only when needed, `never` stays on HTTP, `always` renders every page |
| `--profile` | none | Use a saved signed-in browser profile; every page loads in Patchright |
| `--verbose` | off | Print the full JSON report: sources, page outcomes, and crawl details |

### `pagelode emails-bulk`

Crawls many sites in parallel and writes the results to SQLite. The input is a file with one URL or domain per line, or `-` for standard input. Invalid and duplicate lines are skipped.

| Option | Default | Effect |
|---|---|---|
| `--db` | `data/emails.sqlite` | SQLite file to create or extend |
| `--workers` | `32` | Sites crawled in parallel, 1–512 |
| `--max-pages` | `8` | Pages to visit per site, 1–100 |
| `--max-emails` | `50` | Stop a site after this many unique addresses, 1–1000 |
| `--max-duration` | `20s` | Crawl time budget per site, 1s–2m |
| `--render` | `never` | `auto`, `never`, or `always`; `never` stays on fast HTTP |

The `sites` table holds one row per crawled site with its outcome, page counts, duration, and any error. The `emails` table holds one row per address and source page. Sites already in `sites` are skipped, so interrupting with Ctrl-C and rerunning resumes. Delete the database to start over.

### `pagelode profile login <name> <URL>`

Opens a visible browser session on the browser service at the URL and prints its CDP URL. The session lasts up to one hour. Sign in in that window, then press Enter in the terminal to save the profile under `<name>` and close the session. Use it later with `--profile <name>` on `discover` or `emails`. The browser service stores profiles in `BROWSER_SERVICE_PROFILES_DIR`.

```sh
pagelode profile login account https://example.com/login
pagelode discover --profile account example.com
pagelode emails --profile account https://example.com/directory
```

### `pagelode serve`

Starts the HTTP API on `PORT` (default `8083`).

### Exit status

`0` on success. `1` on any error, including a failed extraction or a discovery whose outcome is not `ok`. The JSON output is still printed before a failed `--json` extraction or discovery exits. `emails` exits `0` on a partial crawl and `1` only when every page failed. `map` exits `1` only when it found no URL and could not load the site.

## Run as a service

```sh
./bin/pagelode serve
```

The API starts at `http://localhost:8083`. Send it a page:

```sh
curl -sS http://localhost:8083/extract \
  -H 'content-type: application/json' \
  -d '{"url":"https://example.com"}'
```

The response contains:

- `content`: the page as clean Markdown
- `title`: the page title
- `links`: same-site links found on the page
- `provider`: the loader that succeeded
- `attempts`: what PageLode tried and why it escalated

The API serves the same operations as the CLI:

| Endpoint | CLI equivalent | Body |
|---|---|---|
| `POST /extract` | `pagelode <URL>` | `{"url"}` |
| `POST /map` | `pagelode map` | `{"url", "limit", "maxDurationMs", "subdomains", "include", "exclude", "depth", "render", "explore"}` |
| `POST /discover` | `pagelode discover` | `{"url", "waitMs", "profile", "stealth", "verbose"}` or `{"har"}` for an offline capture |
| `POST /emails` | `pagelode emails` | `{"url", "maxPages", "maxEmails", "maxDurationMs", "profile", "render"}` |
| `GET /healthz` | none | none |

`emails-bulk`, `profile login`, and `version` have no API form.

## Map a site

```sh
pagelode map example.com
pagelode map --depth 3 --limit 2000 example.com
pagelode map --include '/blog/*' --exclude '/blog/tag/*' example.com
pagelode map --render auto --explore https://app.example.com
pagelode map --json example.com
```

Or use the API:

```sh
curl -sS http://localhost:8083/map \
  -H 'content-type: application/json' \
  -d '{"url":"https://example.com","depth":3,"render":"auto"}'
```

`--json` returns the URLs with the source that found each one, the page templates, and the endpoint paths found in scripts:

```json
{
  "url": "https://example.com/",
  "outcome": "ok",
  "urls": [
    { "url": "https://example.com/pricing", "source": "sitemap" },
    { "url": "https://example.com/blog/hello", "source": "link" }
  ],
  "summary": { "urlsFound": 214, "sitemapsRead": 2, "pagesCrawled": 61, "pagesRendered": 3, "robotsSkipped": 4, "durationMs": 5210, "limited": false },
  "endpoints": ["https://example.com/api/products"],
  "templates": [{ "pattern": "/blog/*", "count": 120, "samples": ["https://example.com/blog/hello"] }]
}
```

Scrape one sample of each template rather than every URL. Depth, sampling, robots rules, and rendering are described under [`pagelode map`](#pagelode-map). `scripts/discovery-bench.sh <site>` compares a map with Katana, when it is installed.

## Discover page data endpoints

```sh
./bin/pagelode discover https://example.com/listings
./bin/pagelode discover --har capture.har
```

Or use the API:

```sh
curl -sS http://localhost:8083/discover \
  -H 'content-type: application/json' \
  -d '{"url":"https://example.com/listings","waitMs":1500}'
```

Discovery captures the page's network traffic and returns endpoint groups, request and response field types, authentication signals, pagination candidates, and optional sanitized evidence with `--verbose`. [Discovery documentation](docs/discovery.md) explains the response and capture limits.

## Find email addresses

```sh
pagelode emails example.com
pagelode emails --max-pages 10 --max-duration 15s example.com
pagelode emails --profile account https://app.example.com
```

Or send `POST /emails` with `{"url":"example.com"}`. The CLI prints one deduplicated email address per line. Add `--verbose` for sources and crawl details. The crawler prioritizes contact and team pages and reads full HTML and captured JSON. See [email finding](docs/emails.md) for limits, rendering controls, authenticated searches, and the response format.

## Docker

```sh
BROWSER_SERVICE_TOKEN=change-me docker compose up --build
```

Compose starts two containers that share `BROWSER_SERVICE_TOKEN`:

| Service | Image | Port |
|---|---|---|
| `pagelode` | Go binary in service mode; learned routes, clearances, and identities persist in the `pagelode-data` volume at `/data` | `8083` |
| `browser` | Bun browser service with headed Google Chrome on an Xvfb virtual display | `127.0.0.1:8090` |

Browser data and profiles persist in the `browser-data` volume at `/data`. `BROWSER_TZ` sets the browser container's timezone (default `Etc/UTC`). See [browser service](docs/browser-service.md) for the API, sessions, identities, and the container.

## Current scope

PageLode currently supports HTML and text pages. It detects common block pages and JavaScript-only shells, limits response sizes and concurrency, and remembers the best loader and browser clearances per domain in a local SQLite file that survives restarts.

It does not guarantee access to protected websites. The optional CapSolver fallback requires an API key and a sticky authenticated proxy. Some sites also require authenticated sessions or permission from the site owner. Use PageLode responsibly and follow applicable terms, robots policies, and laws.

## Documentation

- [Architecture](docs/architecture.md)
- [Browser service](docs/browser-service.md)
- [Browser stack decision](docs/browser-stack-decision.md)
- [Browser routing](docs/browser-routing.md)
- [What PageLode can take from Katana](docs/katana.md)
- [Endpoint discovery](docs/discovery.md)
- [Email finding](docs/emails.md)
- [Configuration](docs/configuration.md)
- [Cloudflare fallback status](docs/cloudflare-status.md)
- [OpenExtract migration plan](docs/migration.md)
- [Plan beyond Markdown extraction](docs/generalization-plan.md)

## Development

```sh
make check
make smoke
```

`make check` runs the Go tests, race detector, vet, and the browser service type check (`bun run check` in `browser-service/`). `make smoke` starts the browser service and PageLode, verifies the HTTP and browser paths against local fixtures, and checks that a second CLI run reuses the persisted route. `scripts/stealth-check.sh` renders bot-detector.rebrowser.net through the service and fails on any red check.

## License

MIT

## Roadmap

See the [feature roadmap](docs/roadmap.md) for planned interactive sessions, authenticated scans, crawling, structured records, endpoint collection, jobs, rendering controls, additional outputs, caching, monitoring, search, and integrations.
