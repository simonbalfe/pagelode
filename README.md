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

### Authenticated discovery

```sh
pagelode profile login account https://example.com/login
pagelode discover --profile account example.com
```

Sign in in the opened Patchright browser and press Enter in the terminal to save the profile and close it. Go reuses it for later discovery. Add `--verbose` for request evidence and matching scores. See [discovery documentation](docs/discovery.md) for API usage, profile storage, and endpoint matching rules.

## Roadmap

See the [feature roadmap](docs/roadmap.md) for planned interactive sessions, authenticated scans, crawling, structured records, endpoint collection, jobs, rendering controls, additional outputs, caching, monitoring, search, and integrations.
