# Configuration

PageLode is configured with environment variables. Defaults are suitable for local development.

PageLode has two processes: the Go service (`PAGELODE_*` variables) and the browser service in `browser-service/` that runs Patchright (`BROWSER_SERVICE_*` variables). Go reaches the browser service over HTTP.

## PageLode

| Variable | Default | Purpose |
|---|---:|---|
| `PORT` | `8083` | HTTP server port |
| `PAGELODE_BROWSER_URL` | `http://127.0.0.1:8090` | Base URL of the browser service (`http` or `https`) |
| `PAGELODE_BROWSER_TOKEN` | empty | Bearer token sent to the browser service; must match `BROWSER_SERVICE_TOKEN` |
| `PAGELODE_PROXY_URL` | empty | Shared HTTP, HTTPS, SOCKS4, or SOCKS5 proxy for every loader |
| `CAPSOLVER_API_KEY` | empty | Enable CapSolver: the Cloudflare fallback, Google `/sorry`, and DataDome sliders. Only the PageLode process reads it |
| `PAGELODE_CAPSOLVER_URL` | `https://api.capsolver.com` | CapSolver API base URL |
| `PAGELODE_CAPSOLVER_PROXY_URL` | shared proxy or empty | Sticky authenticated proxy used for solving and the solved request |
| `PAGELODE_PROTECTED_DOMAINS` | `crunchbase.com` | Comma-separated hosts routed directly to Patchright |
| `PAGELODE_CHROMEDP_ENABLED` | `true` | Enable the ordinary JavaScript-rendering layer |
| `PAGELODE_MAX_CONCURRENCY` | `20` | Maximum active extraction requests |
| `PAGELODE_BROWSER_CONCURRENCY` | `4` | Maximum active Chromedp and Patchright jobs |
| `PAGELODE_MAX_WAITING` | `100` | Maximum requests waiting for either limiter |
| `PAGELODE_REQUEST_TIMEOUT` | `90s` | Deadline for a complete extraction |
| `PAGELODE_ROUTE_TTL` | `30m` | Lifetime of a learned hostname route |

`PAGELODE_BROWSER_CONCURRENCY` cannot exceed `PAGELODE_MAX_CONCURRENCY`.

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

PageLode works with any proxy provider. Set one URL and every loader (HTTP, Chromedp, Patchright) uses it:

```sh
PAGELODE_PROXY_URL=http://username:password@proxy.example.com:8080
```

- Accepted schemes are `http`, `https`, `socks4`, and `socks5`.
- IP-allowlisted proxies work without `username:password@`.
- Prefer the provider's HTTP endpoint. Chrome does not support authenticated SOCKS5, so a `socks5://user:pass@…` proxy fails in Patchright and Chromedp.
- Providers that use `host:port:user:pass` strings must be rewritten as `http://user:pass@host:port`.

### Use a sticky session

A browser page makes many connections. If the provider gives each connection a new IP, Cloudflare sees one page load from several addresses and keeps challenging. Configure the provider's sticky session so one IP serves the whole run. How to request one differs by provider. It is usually a session parameter in the username or password; check the provider's documentation.

The proxy's country does not need to match the machine's timezone. For an HTTP or HTTPS proxy, the browser service looks up the proxy's exit IP and launches Chrome with a matching timezone and languages, and turns off non-proxied WebRTC UDP. See [browser service](browser-service.md).

### Fresh session per run

When the proxy password contains `_session-<id>`, `_hardsession-<id>`, or `_lockedsession-<id>`, PageLode replaces the ID with a random one at startup. Every CLI run gets a new sticky IP, and all loaders in that run share it. A long-running server keeps one session until it restarts or the provider's session lifetime expires.

```sh
PAGELODE_PROXY_URL=http://username:password_session-any1234_lifetime-30@proxy.example.com:3000
```

Other session formats, such as a session ID in the username, are used unchanged. Change the ID yourself to get a new IP.

The browser service also replaces a `{session}` placeholder in the proxy URL with a stable ID per browser identity, profile, or session, so each one keeps its own sticky IP and rotating an identity gets a new one. Write it URL-encoded (`%7Bsession%7D`) in `PAGELODE_PROXY_URL`; the Go loaders use the URL as written. See [browser service](browser-service.md).

### CapSolver proxy

`PAGELODE_CAPSOLVER_PROXY_URL` sets a separate proxy for the CapSolver fallback only. If it is unset, CapSolver uses `PAGELODE_PROXY_URL`. CapSolver needs a sticky proxy with a username and password, because its clearance cookie is bound to the IP that solved the challenge. PageLode replays the solved cookies and user agent through that same proxy.

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

Protected domains begin at Patchright instead of spending time on the HTTP and Chromedp layers.

```sh
PAGELODE_PROTECTED_DOMAINS=example.com,another.example
```

A rule for a parent domain also matches its subdomains.

## Production baseline

For a private deployment, place PageLode behind an authenticated reverse proxy, set explicit concurrency values for the available memory and CPU, and avoid exposing port 8083 directly to the public internet. Keep the browser service private as well: set the same token in `BROWSER_SERVICE_TOKEN` and `PAGELODE_BROWSER_TOKEN`, and do not publish port 8090 beyond the host.

## Authenticated discovery

Use `pagelode profile login <name> <URL>` to create a persistent Patchright login and `pagelode discover --profile <name> <URL>` to reuse it. Login opens a visible browser on the browser service host; discovery with a profile uses the service's `BROWSER_SERVICE_HEADLESS` setting. Profiles are stored by the browser service under `BROWSER_SERVICE_PROFILES_DIR`. See [endpoint discovery](discovery.md#authenticated-profiles) for session storage and API usage.
