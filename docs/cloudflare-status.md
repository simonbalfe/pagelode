# Cloudflare status

Status recorded on 5 October 2026.

## How PageLode handles a challenge

```mermaid
flowchart LR
    Target[Protected page] --> HTTP[HTTP request]
    HTTP -->|Challenge| Patchright[Patchright: real Chrome]
    Patchright -->|Still blocked| Bootstrap[Fetch challenge through sticky proxy]
    Bootstrap --> CapSolver[CapSolver AntiCloudflareTask]
    CapSolver --> Session[Clearance cookie + matching user agent]
    Session --> Chromedp[Chromedp through the same proxy]
    Chromedp --> Classifier[Classify and extract]
```

Patchright is the primary path. CapSolver is an optional last resort, enabled only when `CAPSOLVER_API_KEY` and either `PAGELODE_CAPSOLVER_PROXY_URL` or `PAGELODE_PROXY_URL` are set.

## Requirements

Patchright passes Cloudflare's standard challenge page when:

1. Chrome runs with a visible window: `BROWSER_SERVICE_HEADLESS=false` (the browser service default).
2. Locale and timezone are not emulated, because Cloudflare detects emulated values. Without a proxy, Chrome keeps the machine's values. With an HTTP or HTTPS proxy, the browser service launches Chrome with the timezone and languages of the proxy's exit IP.
3. The connection uses no proxy, or a sticky proxy session that keeps one IP for the whole run.

The proxy can come from any provider. Setup instructions are in [configuration](configuration.md#proxies).

## Test results

Target: `https://www.scrapingcourse.com/cloudflare-challenge`, a public Cloudflare test page. CapSolver disabled.

| Setup | Result |
|---|---|
| No proxy, visible Chrome | 8 of 8 passed in 1.5 to 3.4 seconds |
| No proxy, headless Chrome | 0 of 3 passed |
| Sticky mobile proxy, any country, visible Chrome | 2 of 2 passed in 7 to 10 seconds |
| Sticky mobile proxy, UK exit, visible Chrome | 2 of 2 passed in 10 to 21 seconds |
| Sticky mobile proxy, US exit, visible Chrome | 2 of 2 passed in about 9 seconds |
| Proxy with a new IP for every connection | 0 of 1 passed |

Before the locale and timezone override was removed, Patchright failed every run, with or without a proxy.

## CapSolver results

CapSolver returned a `cf_clearance` cookie on every task, but Cloudflare accepted it on only 1 of 9 replays. Using the same sticky IP, the exact solved user agent, and a Chrome TLS fingerprint did not change that. The fallback is unreliable and should not be relied on when Patchright can pass.

CapSolver rejects proxies it cannot authenticate before solving. It requires a sticky proxy with a username and password.

## Known gaps

- The browser service container now runs headed Google Chrome on an Xvfb virtual display. Container results are in [browser service](browser-service.md), not in the table above.
- With a `_session-<id>` proxy, each PageLode server process keeps one proxy session until restart. Only CLI runs get a new session automatically. A `{session}` placeholder gives each browser service identity its own session, and rotating the identity gets a new one; see [configuration](configuration.md#fresh-session-per-run).
- Crunchbase has not been retested since the locale fix.

## Test command

```sh
BROWSER_SERVICE_DEBUG=true make run-browser
```

```sh
PAGELODE_DEBUG=true pagelode --json https://www.scrapingcourse.com/cloudflare-challenge
```

`PAGELODE_DEBUG` output contains CapSolver provider, status, timing, cookie count, and browser major version without secret values. `BROWSER_SERVICE_DEBUG` logs browser service events such as navigation, challenge detection, and identity launches.
