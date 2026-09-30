# Configuration

PageLode is configured with environment variables. Defaults are suitable for local development.

| Variable | Default | Purpose |
|---|---:|---|
| `PORT` | `8083` | HTTP server port |
| `PAGELODE_PATCHRIGHT_COMMAND` | `bun` | Program used to run the Patchright worker |
| `PAGELODE_PATCHRIGHT_WORKER` | `browser/src/worker.ts` | Worker entry point |
| `PAGELODE_PROFILES_DIR` | user configuration directory plus `pagelode/profiles` | Named authenticated discovery profiles |
| `PAGELODE_PATCHRIGHT_HEADLESS` | `true` | Run Patchright without a visible window |
| `PAGELODE_PROXY_URL` | empty | Shared HTTP, HTTPS, SOCKS4, or SOCKS5 proxy |
| `CAPSOLVER_API_KEY` | empty | Enable the managed Cloudflare fallback |
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

## Proxy format

Accepted schemes are `http`, `https`, `socks4`, and `socks5`.

```sh
PAGELODE_PROXY_URL=http://username:password@proxy.example:8080
```

The shared proxy is applied to every loader. `PAGELODE_CAPSOLVER_PROXY_URL` can instead provide a proxy only for managed Cloudflare solving. If it is unset, PageLode uses `PAGELODE_PROXY_URL`.

CapSolver requires a sticky authenticated proxy because the challenge solution is bound to the network identity. PageLode passes the solved cookies and user agent to Chromedp and repeats the request through that exact proxy.

## Protected domains

Protected domains begin at Patchright instead of spending time on the HTTP and Chromedp layers.

```sh
PAGELODE_PROTECTED_DOMAINS=example.com,another.example
```

A rule for a parent domain also matches its subdomains.

## Production baseline

For a private deployment, place PageLode behind an authenticated reverse proxy, set explicit concurrency values for the available memory and CPU, and avoid exposing port 8083 directly to the public internet.

## Authenticated discovery

Use `pagelode profile login <name> <URL>` to create a persistent Patchright login and `pagelode discover --profile <name> <URL>` to reuse it. Named profiles use a visible browser for login and headless discovery. See [endpoint discovery](discovery.md#authenticated-profiles) for session storage and API usage.
