# PageLode

PageLode extracts clean Markdown, maps a site's page URLs, discovers the data endpoints behind web pages, and finds published email addresses.

Give it a URL and it returns the useful content, page title, internal links, and a record of how the page was loaded. It begins with a fast direct request and only opens a browser when the page actually needs one.

## Why PageLode?

Many web pages can be downloaded directly. Others need JavaScript, and some place a browser check in front of their content. Using a full browser for every request is slow and expensive, so PageLode uses a simple waterfall:

1. Load the page with a browser-like HTTP client.
2. If JavaScript is required, render it with Chromedp and Chromium.
3. If the page appears blocked, retry it with Patchright.
4. If Cloudflare still blocks the page and CapSolver is configured, obtain a valid browser session and retry through the same proxy.
5. Remove navigation, advertising, and other clutter.
6. Return readable Markdown and useful metadata.

PageLode keeps the main service in Go. Patchright, whose browser tooling is built for the JavaScript ecosystem, runs in a separate Bun browser service (`browser-service/`) that Go calls over HTTP.

## Quick start

You need Go, Bun, and Chromium installed. On macOS and Windows the browser service uses Google Chrome by default, so install Chrome as well.

```sh
make setup
make build
make run-browser   # in a second terminal
./bin/pagelode example.com
```

PageLode prints the cleaned page as Markdown. The browser service listens on `http://127.0.0.1:8090`; PageLode calls it only when a page needs Patchright.

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
pagelode map [--limit 500] [--max-duration 30s] [--include <path>] [--exclude <path>] [--subdomains] [--json] <URL>
pagelode emails [--max-pages 20] [--max-emails 100] [--max-duration 30s] [--render auto] [--profile <name>] [--verbose] <URL>
pagelode emails-bulk [--db data/emails.sqlite] [--workers 32] [--max-pages 8] [--max-emails 50] [--max-duration 20s] [--render never] <file|->
pagelode profile login <name> <URL>
pagelode serve
pagelode version
pagelode help
```

Put options before the URL. A bare domain such as `example.com` is accepted wherever a URL is. Every command reads its settings from environment variables; see [configuration](docs/configuration.md) for proxies, browser mode, CapSolver, and limits.

### `pagelode <URL>` / `pagelode extract <URL>`

Loads one page through the waterfall and prints clean Markdown.

| Option | Default | Effect |
|---|---|---|
| `--json`, `-j` | off | Print the complete result: content, title, links, provider, and every loader attempt |

### `pagelode discover`

Captures a page's network traffic and prints a JSON report of its data endpoints.

| Option | Default | Effect |
|---|---|---|
| `--wait-ms` | `1500` | Observe network activity after page load, 100–10000 ms |
| `--profile` | none | Use a saved signed-in browser profile |
| `--stealth` | off | Skip Chromedp and load only with the Patchright stealth browser through `PAGELODE_PROXY_URL`. Fails when no proxy is configured |
| `--verbose` | off | Include request evidence, matching scores, and loading details |
| `--har` | none | Analyze a HAR file instead of opening a browser. An optional URL names the page it came from |

### `pagelode map`

Lists the page URLs of one site, one per line. It reads `robots.txt` sitemaps, follows sitemap indexes (up to 25 sitemaps), falls back to `/sitemap.xml` and `/sitemap_index.xml`, then adds the start page's links. It uses plain HTTP only, with no browser. URLs are canonicalized (fragments and tracking parameters removed), deduplicated, and limited to the site's host and its `www` twin. Compressed `.xml.gz` sitemaps are skipped with a warning.

| Option | Default | Effect |
|---|---|---|
| `--limit` | `500` | Maximum URLs, 1–5000 |
| `--max-duration` | `30s` | Time budget, 1s–2m |
| `--include` | all | Keep only URLs whose path matches this pattern, such as `/blog/*`; `*` matches any characters. Repeatable |
| `--exclude` | none | Drop URLs whose path matches this pattern. Wins over `--include`. Repeatable |
| `--subdomains` | off | Also include subdomains of the site |
| `--json` | off | Print the report: each URL with its `source` (`sitemap` or `page`) and `lastModified`, plus sitemap counts and warnings |

Exits `1` only when no URL was found and nothing could be loaded. `POST /map` takes `{"url", "limit", "maxDurationMs", "subdomains", "include", "exclude"}` and returns the same report.

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

`0` on success. `1` on any error, including a failed extraction or a discovery whose outcome is not `ok`. The JSON output is still printed before a failed `--json` extraction or discovery exits. `emails` exits `0` on a partial crawl and `1` only when every page failed.

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
| `pagelode` | Go binary plus Debian Chromium for Chromedp, in service mode | `8083` |
| `browser` | Bun browser service with headed Google Chrome on an Xvfb virtual display | `127.0.0.1:8090` |

Browser data and profiles persist in the `browser-data` volume at `/data`. `BROWSER_TZ` sets the browser container's timezone (default `Etc/UTC`). See [browser service](docs/browser-service.md) for the API, sessions, identities, and the container.

## Current scope

PageLode currently supports HTML and text pages. It detects common block pages and JavaScript-only shells, limits response sizes and concurrency, and remembers the best loader for recently visited domains.

It does not guarantee access to protected websites. The optional CapSolver fallback requires an API key and a sticky authenticated proxy. Some sites also require authenticated sessions or permission from the site owner. Use PageLode responsibly and follow applicable terms, robots policies, and laws.

## Documentation

- [Architecture](docs/architecture.md)
- [Browser service](docs/browser-service.md)
- [Browser routing](docs/browser-routing.md)
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

`make check` runs the Go tests, race detector, vet, and the browser service type check (`bun run check` in `browser-service/`). `make smoke` starts the browser service and PageLode, then verifies all three loading paths against local fixtures.

## License

MIT

## Roadmap

See the [feature roadmap](docs/roadmap.md) for planned interactive sessions, authenticated scans, crawling, structured records, endpoint collection, jobs, rendering controls, additional outputs, caching, monitoring, search, and integrations.
