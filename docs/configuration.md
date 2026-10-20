# Configuration

PageLode is configured with environment variables. Defaults are suitable for local development.

PageLode has two processes: the Go service (`PAGELODE_*` variables) and the browser service in `browser-service/` that runs Patchright (`BROWSER_SERVICE_*` variables). Go reaches the browser service over HTTP.

## PageLode

| Variable | Default | Purpose |
|---|---:|---|
| `PORT` | `8083` | HTTP server port |
| `PAGELODE_BROWSER_URL` | `http://127.0.0.1:8090` | Base URL of the browser service (`http` or `https`). A comma-separated list spreads renders over several browser service nodes |
| `PAGELODE_BROWSER_TOKEN` | empty | Bearer token sent to every browser service node; must match `BROWSER_SERVICE_TOKEN` |
| `PAGELODE_BROWSER_IDENTITIES` | `1` | Browser identities, each with its own proxy session. Hosts are spread over them by a stable hash, each rotates on its own, and identity `n` renders on node `n` modulo the node count |
| `PAGELODE_BROWSER_NODE_SLOTS` | `4` | Renders PageLode sends to one browser service node at once; match that node's `BROWSER_SERVICE_CONCURRENCY` |
| `PAGELODE_QUEUE_URL` | empty | Postgres URL (`postgres://` or `postgresql://`) of the shared queue used by `pagelode batch` |
| `PAGELODE_PROXY_URL` | empty | Shared HTTP, HTTPS, SOCKS4, or SOCKS5 proxy template for every loader; see [proxies](#proxies) |
| `CAPSOLVER_API_KEY` | empty | Enable CapSolver: the Cloudflare fallback, Google `/sorry`, and DataDome sliders. Only the PageLode process reads it |
| `PAGELODE_CAPSOLVER_URL` | `https://api.capsolver.com` | CapSolver API base URL |
| `PAGELODE_CAPSOLVER_PROXY_URL` | shared proxy or empty | Sticky authenticated proxy used for solving and the solved request |
| `PAGELODE_PROTECTED_DOMAINS` | `crunchbase.com` | Comma-separated hosts routed directly to Patchright |
| `PAGELODE_MAX_CONCURRENCY` | `20` | Maximum active extraction requests |
| `PAGELODE_MAX_WAITING` | `100` | Maximum extraction requests waiting for a slot |
| `PAGELODE_REQUEST_TIMEOUT` | `90s` | Deadline for a complete extraction |
| `PAGELODE_ROUTE_TTL` | `30m` | Lifetime of a learned hostname route |
| `PAGELODE_BROWSER_SETTLE_MS` | `2000` | Longest wait for a browser render to go quiet after load, 0–10000 ms; sent as `settle_ms`. Quiet pages return sooner |
| `PAGELODE_STATE_PATH` | user configuration directory plus `pagelode/state.sqlite` | SQLite file for learned routes, clearances, and identity generations |

PageLode sends at most `PAGELODE_BROWSER_NODE_SLOTS` renders to each browser service node at once; further renders wait for a slot within their deadline. The service also limits itself (`BROWSER_SERVICE_CONCURRENCY`, `BROWSER_SERVICE_MAX_WAITING`). When its queue is full it answers `429 saturated`; PageLode records an `error` attempt. A node that cannot be reached is skipped for 15 seconds and the render is retried once on the next node; when no node is reachable the render fails with `limit.ErrUnavailable`.

### State file

`PAGELODE_STATE_PATH` defaults to `~/Library/Application Support/pagelode/state.sqlite` on macOS and `~/.config/pagelode/state.sqlite` on Linux. It holds learned routes, clearances (cookies, browser headers, proxy URL), and the current identity generation per proxy, so routes and clearances survive restarts and separate CLI runs. The file is created with mode `0600` because clearances are private. Rows are loaded at start, expired rows are pruned, and every change is written through; a failed write is logged and does not stop the request. The Docker image sets it to `/data/state.sqlite` on the `pagelode-data` volume.

## Browser service

The browser service owns the browser mode, Chrome channel, and saved profiles. It holds no CapSolver key. The main settings:

| Variable | Default | Purpose |
|---|---:|---|
| `BROWSER_SERVICE_HOST` | `127.0.0.1` | Listen address |
| `BROWSER_SERVICE_PORT` | `8090` | Listen port |
| `BROWSER_SERVICE_TOKEN` | empty | Bearer token for `/v1/*`. Required when the host is not a loopback address |
| `BROWSER_SERVICE_HEADLESS` | `false` | Run Chrome without a visible window. Cloudflare challenges only pass with `false` |
| `BROWSER_SERVICE_CHANNEL` | `chrome` on macOS and Windows, bundled Chromium on Linux | Patchright browser channel. Set it empty to use bundled Chromium |
| `BROWSER_SERVICE_PROFILES_DIR` | user configuration directory plus `pagelode/profiles` | Named authenticated discovery profiles |
| `BROWSER_SERVICE_DATA_DIR` | user data directory plus `PageLode/Patchright` (`pagelode/patchright` on Linux) | Browser data for the warm identity pool |
| `BROWSER_SERVICE_DEBUG` | `false` | Log browser events as JSON on stderr |

See [browser service](browser-service.md) for every variable, the HTTP API, sessions, and identities.

## Proxies

PageLode works with any proxy provider. Set one URL and every loader (HTTP, Patchright, CapSolver) uses it:

```sh
PAGELODE_PROXY_URL=http://username:password@proxy.example.com:8080
```

- Accepted schemes are `http`, `https`, `socks4`, and `socks5`.
- IP-allowlisted proxies work without `username:password@`.
- Prefer the provider's HTTP endpoint. Chrome does not support authenticated SOCKS5, so a `socks5://user:pass@…` proxy fails in Patchright.
- Providers that use `host:port:user:pass` strings must be rewritten as `http://user:pass@host:port`.

### Use a sticky session

A browser page makes many connections. If the provider gives each connection a new IP, Cloudflare sees one page load from several addresses and keeps challenging. Configure the provider's sticky session so one IP serves the whole run. How to request one differs by provider. It is usually a session parameter in the username or password; check the provider's documentation.

The proxy's country does not need to match the machine's timezone. For an HTTP or HTTPS proxy, the browser service looks up the proxy's exit IP and launches Chrome with a matching timezone and languages, and turns off non-proxied WebRTC UDP. See [browser service](browser-service.md).

### Proxy sessions

`PAGELODE_PROXY_URL` is a template. PageLode owns the sticky session in it when the URL contains either:

- a `{session}` placeholder, written raw or URL-encoded (`%7Bsession%7D`), or
- a provider password token `_session-<id>`, `_hardsession-<id>`, or `_lockedsession-<id>`.

```sh
PAGELODE_PROXY_URL=http://username:password_session-any1234_lifetime-30@proxy.example.com:3000
```

PageLode derives the session from the browser identity, not from the run:

| Use | Session |
|---|---|
| Anonymous renders and plain HTTP | Derived from the identity key `pl-<hash of template>-g<generation>`; stable across runs |
| Saved profiles | Derived from `profile:<name>` |
| Ad-hoc browser sessions | Random per session |

Plain HTTP follows the current identity's proxy, so HTTP and the browser share one exit IP. When one host blocks the identity on 2 consecutive renders (status 401, 403, or 429, or a remaining challenge), PageLode moves to the next generation, which gives a new session and IP, stores it in the state file, and asks the browser service to delete the old identity.

Other session formats, such as a session ID in the username without `{session}`, are used unchanged and never rotate. Change the ID yourself to get a new IP.

The browser service receives only concrete proxy URLs and rejects any containing `{session}`. See [browser service](browser-service.md#identities-and-rotation).

### CapSolver proxy

`PAGELODE_CAPSOLVER_PROXY_URL` sets a separate proxy for the CapSolver fallback only. If it is unset, CapSolver uses `PAGELODE_PROXY_URL`. CapSolver needs a sticky proxy with a username and password, because its clearance cookie is bound to the IP that solved the challenge. Its session is filled with a random ID once per process. PageLode fetches the challenge and the solved page over the HTTP client through that same proxy.

## Passing Cloudflare challenges

Patchright passes Cloudflare's standard challenge page without CapSolver when all of these hold:

1. `BROWSER_SERVICE_HEADLESS=false` (the default), so Chrome runs with a visible window. Headless bundled Chromium stays blocked; its user agent contains `HeadlessChrome`.
2. Google Chrome is installed and `BROWSER_SERVICE_CHANNEL` is `chrome` (the default on macOS and Windows).
3. Either no proxy, or a sticky proxy as described above.

Run it locally with the browser service in one terminal and PageLode in another:

```sh
make run-browser
```

```sh
PAGELODE_PROXY_URL='http://username:password@proxy.example.com:8080' \
pagelode --json https://www.scrapingcourse.com/cloudflare-challenge
```

A pass returns `"outcome": "ok"` with a `patchright` attempt marked `ok`. Leave out `PAGELODE_PROXY_URL` to test without a proxy.

The browser service Docker image runs headed Google Chrome on an Xvfb virtual display (`BROWSER_SERVICE_HEADLESS=false`, `BROWSER_SERVICE_CHANNEL=chrome`). See [Cloudflare status](cloudflare-status.md) for test results and [browser service](browser-service.md) for the container.

## Protected domains

Protected domains go straight to the browser service instead of spending time on an HTTP attempt.

```sh
PAGELODE_PROTECTED_DOMAINS=example.com,another.example
```

A rule for a parent domain also matches its subdomains.

## Production baseline

For a private deployment, place PageLode behind an authenticated reverse proxy, set explicit concurrency values for the available memory and CPU, and avoid exposing port 8083 directly to the public internet. Keep the browser service private as well: set the same token in `BROWSER_SERVICE_TOKEN` and `PAGELODE_BROWSER_TOKEN`, and do not publish port 8090 beyond the host.

## Authenticated discovery

Use `pagelode profile login <name> <URL>` to create a persistent Patchright login and `pagelode discover --profile <name> <URL>` to reuse it. Login opens a visible browser on the browser service host; discovery with a profile uses the service's `BROWSER_SERVICE_HEADLESS` setting. Profiles are stored by the browser service under `BROWSER_SERVICE_PROFILES_DIR`. See [endpoint discovery](discovery.md#authenticated-profiles) for session storage and API usage.
