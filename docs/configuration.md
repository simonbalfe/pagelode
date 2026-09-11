# Configuration

PageLode is configured with environment variables. Defaults are suitable for local development.

| Variable | Default | Purpose |
|---|---:|---|
| `PORT` | `8083` | HTTP server port |
| `PAGELODE_PATCHRIGHT_COMMAND` | `bun` | Program used to run the Patchright worker |
| `PAGELODE_PATCHRIGHT_WORKER` | `browser/src/worker.ts` | Worker entry point |
| `PAGELODE_PATCHRIGHT_HEADLESS` | `true` | Run Patchright without a visible window |
| `PAGELODE_PROXY_URL` | empty | Shared HTTP, HTTPS, SOCKS4, or SOCKS5 proxy |
| `PAGELODE_PROTECTED_DOMAINS` | `crunchbase.com` | Comma-separated hosts routed directly to Patchright |
| `PAGELODE_ROD_ENABLED` | `true` | Enable the ordinary JavaScript-rendering layer |
| `PAGELODE_MAX_CONCURRENCY` | `20` | Maximum active extraction requests |
| `PAGELODE_BROWSER_CONCURRENCY` | `4` | Maximum active Rod and Patchright jobs |
| `PAGELODE_MAX_WAITING` | `100` | Maximum requests waiting for either limiter |
| `PAGELODE_REQUEST_TIMEOUT` | `90s` | Deadline for a complete extraction |
| `PAGELODE_ROUTE_TTL` | `30m` | Lifetime of a learned hostname route |

`PAGELODE_BROWSER_CONCURRENCY` cannot exceed `PAGELODE_MAX_CONCURRENCY`.

## Proxy format

Accepted schemes are `http`, `https`, `socks4`, and `socks5`.

```sh
PAGELODE_PROXY_URL=http://username:password@proxy.example:8080
```

The proxy is applied to all three loaders so the HTTP and browser attempts keep a coherent network identity.

## Protected domains

Protected domains begin at Patchright instead of spending time on the HTTP and Rod layers.

```sh
PAGELODE_PROTECTED_DOMAINS=example.com,another.example
```

A rule for a parent domain also matches its subdomains.

## Production baseline

For a private deployment, place PageLode behind an authenticated reverse proxy, set explicit concurrency values for the available memory and CPU, and avoid exposing port 8083 directly to the public internet.
