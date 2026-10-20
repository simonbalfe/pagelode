# PageLode

PageLode extracts clean Markdown, maps a site's page URLs, discovers the data endpoints behind web pages, and finds published email addresses.

Each page loads through a waterfall: replay a stored clearance, try a browser-like HTTP request, and only render in a real browser (Patchright, in the Bun `browser-service/`) when the page needs JavaScript or is blocked. Optional CapSolver handles challenges that remain. See [architecture](docs/architecture.md).

## Quick start

You need Go and Bun. On macOS and Windows the browser service uses Google Chrome, so install Chrome as well.

```sh
make setup
make build
make run-browser   # in a second terminal
./bin/pagelode example.com
```

## CLI

| Command | What it does | Output |
|---|---|---|
| `pagelode <URL>` / `extract` | Load one page and clean it | Markdown, or `--json` with links and loader attempts |
| `map` | List a site's pages, page templates, and script endpoints | One URL per line, or `--json` |
| `discover` | Find the data endpoints behind a page | JSON report |
| `emails` | Find published email addresses on a site | One address per line |
| `emails-bulk` | `emails` for a list of sites | SQLite database |
| `batch` | `extract` for millions of URLs across many machines | Gzipped JSONL; queue in Postgres |
| `profile login` | Save a signed-in browser for `--profile` | Named profile |
| `serve` | Run the same operations as an HTTP API | API on `PORT` (default `8083`) |

```sh
pagelode example.com
pagelode map --json example.com
pagelode discover https://example.com/listings
pagelode emails example.com
```

A typical workflow: `map` the site to find its page types, `extract` a sample of each, `discover` the endpoints behind the interesting ones, then `emails` for contacts.

Run `pagelode help <command>` for every flag. Settings come from environment variables; see [configuration](docs/configuration.md).

## Docker

```sh
BROWSER_SERVICE_TOKEN=change-me docker compose up --build
```

Starts the API on `8083` and the browser service on `127.0.0.1:8090`. See [deployment](docs/architecture.md#deployment-model).

## Documentation

- [CLI reference](docs/cli.md): every command, flag, and exit status
- [HTTP API](docs/api.md)
- [Site mapping](docs/map.md)
- [Endpoint discovery](docs/discovery.md)
- [Email finding](docs/emails.md)
- [Batch scraping](docs/batch.md)
- [Configuration](docs/configuration.md)
- [Using PageLode as a library](docs/library.md)
- [Architecture](docs/architecture.md)
- [Browser service](docs/browser-service.md)
- [Browser routing](docs/browser-routing.md)
- [Browser stack decision](docs/browser-stack-decision.md)
- [Cloudflare fallback status](docs/cloudflare-status.md)
- [What PageLode can take from Katana](docs/katana.md)
- [Roadmap](docs/roadmap.md)
- [OpenExtract migration plan](docs/migration.md)
- [Plan beyond Markdown extraction](docs/generalization-plan.md)

## Scope

PageLode supports HTML and text pages. It does not guarantee access to protected websites; the CapSolver fallback needs an API key and a sticky authenticated proxy. Use it responsibly and follow applicable terms, robots policies, and laws.

## Development

```sh
make check   # Go tests, race detector, vet, browser-service type check
make smoke   # end-to-end HTTP and browser paths against local fixtures
```

## License

MIT
