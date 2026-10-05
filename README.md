# PageLode

PageLode extracts clean Markdown, discovers the data endpoints behind web pages, and finds published email addresses.

Give it a URL and it returns the useful content, page title, internal links, and a record of how the page was loaded. It begins with a fast direct request and only opens a browser when the page actually needs one.

## Why PageLode?

Many web pages can be downloaded directly. Others need JavaScript, and some place a browser check in front of their content. Using a full browser for every request is slow and expensive, so PageLode uses a simple waterfall:

1. Load the page with a browser-like HTTP client.
2. If JavaScript is required, render it with Chromedp and Chromium.
3. If the page appears blocked, retry it with Patchright.
4. If Cloudflare still blocks the page and CapSolver is configured, obtain a valid browser session and retry through the same proxy.
5. Remove navigation, advertising, and other clutter.
6. Return readable Markdown and useful metadata.

PageLode keeps the main service in Go. A small TypeScript worker exists only for Patchright, whose browser tooling is built for the JavaScript ecosystem.

## Quick start

You need Go, Bun, and Chromium installed.

```sh
make setup
make build
./bin/pagelode example.com
```

PageLode prints the cleaned page as Markdown.

For the complete result, including the loader attempts:

```sh
./bin/pagelode --json example.com
```

## CLI

```text
pagelode [--json] <URL>
pagelode extract [--json] <URL>
pagelode discover [--wait-ms 1500] [--profile <name>] [--verbose] <URL>
pagelode discover --har <capture.har> [<URL>]
pagelode emails [--max-pages 20] [--max-emails 100] [--max-duration 30s] [--render auto] [--profile <name>] [--verbose] <URL>
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
| `--verbose` | off | Include request evidence, matching scores, and loading details |
| `--har` | none | Analyze a HAR file instead of opening a browser. An optional URL names the page it came from |

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

### `pagelode profile login <name> <URL>`

Opens a visible Patchright browser at the URL. Sign in, then press Enter in the terminal to save the profile under `<name>`. Use it later with `--profile <name>` on `discover` or `emails`. Profiles are stored in `PAGELODE_PROFILES_DIR`.

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
docker compose up --build
```

The container starts PageLode in service mode.

## Current scope

PageLode currently supports HTML and text pages. It detects common block pages and JavaScript-only shells, limits response sizes and concurrency, and remembers the best loader for recently visited domains.

It does not guarantee access to protected websites. The optional CapSolver fallback requires an API key and a sticky authenticated proxy. Some sites also require authenticated sessions or permission from the site owner. Use PageLode responsibly and follow applicable terms, robots policies, and laws.

## Documentation

- [Architecture](docs/architecture.md)
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

`make check` runs the Go tests, race detector, vet, TypeScript checks, and browser-worker tests. `make smoke` verifies all three loading paths against local fixtures.

## License

MIT

## Roadmap

See the [feature roadmap](docs/roadmap.md) for planned interactive sessions, authenticated scans, crawling, structured records, endpoint collection, jobs, rendering controls, additional outputs, caching, monitoring, search, and integrations.
