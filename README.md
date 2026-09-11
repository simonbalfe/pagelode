# PageLode

PageLode turns web pages into clean Markdown.

Give it a URL and it returns the useful content, page title, internal links, and a record of how the page was loaded. It begins with a fast direct request and only opens a browser when the page actually needs one.

## Why PageLode?

Many web pages can be downloaded directly. Others need JavaScript, and some place a browser check in front of their content. Using a full browser for every request is slow and expensive, so PageLode uses a simple waterfall:

1. Load the page with a browser-like HTTP client.
2. If JavaScript is required, render it with Rod and Chromium.
3. If the page appears blocked, retry it with Patchright.
4. Remove navigation, advertising, and other clutter.
5. Return readable Markdown and useful metadata.

PageLode keeps the main service in Go. A small TypeScript worker exists only for Patchright, whose browser tooling is built for the JavaScript ecosystem.

## Quick start

You need Go, Bun, and Chromium installed.

```sh
make setup
make run
```

PageLode starts at `http://localhost:8083`.

Send it a page:

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

## Docker

```sh
docker compose up --build
```

## Current scope

PageLode currently supports HTML and text pages. It detects common block pages and JavaScript-only shells, limits response sizes and concurrency, and remembers the best loader for recently visited domains.

It does not guarantee access to protected websites. Some sites require suitable proxies, authenticated sessions, or permission from the site owner. Use PageLode responsibly and follow applicable terms, robots policies, and laws.

## Documentation

- [Architecture](docs/architecture.md)
- [Configuration](docs/configuration.md)
- [OpenExtract migration plan](docs/migration.md)

## Development

```sh
make check
make smoke
```

`make check` runs the Go tests, race detector, vet, TypeScript checks, and browser-worker tests. `make smoke` verifies all three loading paths against local fixtures.

## License

MIT
