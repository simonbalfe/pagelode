# What PageLode can take from Katana

[Katana](https://github.com/projectdiscovery/katana) is ProjectDiscovery's Go crawler (MIT, about 17.7k stars). This page records what in it is useful to PageLode, read from its source on 19 October 2026. Nothing here is implemented yet; the [roadmap](roadmap.md) owns priorities.

## How Katana is built

| Engine | What it uses | Relevant to PageLode |
|---|---|---|
| Standard | Go HTTP (`retryablehttp-go`, `fastdialer`), optional TLS impersonation (`-tlsi`, `utls`) | Same role as PageLode's tls-client tier |
| Headless | go-rod `v0.116.2` driving Chrome | The Go browser option weighed in the [browser stack decision](browser-stack-decision.md) |
| Hybrid | go-rod with request hijacking: every browser request and response passes through Go | Same idea as the service's network capture |

## The go-rod question

Katana confirms the decision to keep browsers in the Bun service rather than bring go-rod back into Go:

- **Its headless browser is not stealthy.** The default launch flags include `--enable-automation` (only dropped when reusing a profile without incognito), and evasion is the go-rod stealth bundle (puppeteer-extra evasions) injected with `EvalOnNewDocument`. The [stealth measurements](browser-stack-decision.md#stealth-measurements) show `navigator.webdriver` is set by `--enable-automation`, and that rebrowser's `nativeTampering` and `workerConsistency` checks catch injected evasions.
- **What it does well is breadth, not stealth:** fast standard crawling, scope rules, deduplication, and endpoint extraction. Those are Go-side policy and fit PageLode's core.

Worth reusing from its headless code, implemented in the service rather than with go-rod:

| Katana | PageLode use |
|---|---|
| `heuristic` page-load strategy: load event, then a URL-change poll for client-side routing, then network-idle and DOM-stable windows (1 s DOM-stable, 15 s cap) | The service's [quiet wait](browser-service.md#waiting-for-the-page) already covers network and DOM quiet. Add the URL-change poll so single-page apps that route after load are not cut short |
| `-xhr-extraction`: XHR URL and method in output | Already covered by discovery's captured traffic |
| `-recorded-flow`: replay a Chrome DevTools Recorder JSON before crawling | A way to script logins for `pagelode profile`, beyond the manual login session |
| Captcha identification for reCAPTCHA v2/v3/Enterprise, Turnstile, hCaptcha | Extends the service's `challenge` report beyond Cloudflare, DataDome, and reCAPTCHA v2 |

## Endpoint discovery

| Katana feature | What it does | Where it fits in PageLode |
|---|---|---|
| `-jc` JavaScript crawling | Parses fetched JavaScript files for paths and URLs | Discovery today only sees requests the page actually made. Parsing the page's scripts finds endpoints behind clicks and routes not taken during the capture |
| `-jsl` jsluice (BishopFox, Go library) | AST-based extraction of URLs, `fetch`/XHR calls, and secrets from JavaScript | The library to use for the above; Katana marks it memory-intensive, so bound file size and count |
| Knowledge base `-kb-endpoints` | Classifies each request as REST, GraphQL, SOAP, or XHR from method, path, content types, and `SOAPAction`; records auth scheme and parameter names | Matches discovery's endpoint groups and auth signals; compare its `classify` rules with `internal/discovery/classifier.go` and adopt missing GraphQL and SOAP cases |
| `-fx` form extraction | Records forms, inputs, textareas, and selects | Input for the planned authenticated scans and form-driven collection |
| `-kf` known files | Crawls `robots.txt` and `sitemap.xml` | Already covered by `pagelode map` |
| `-td` tech detection (`wappalyzergo`) | Detects frameworks and platforms per page | Pick extraction and discovery strategies per platform (Next.js data, WordPress REST, Shopify JSON) |

## Crawling methods

| Katana feature | What it does | Where it fits in PageLode |
|---|---|---|
| Scope fields `-fs rdn/fqdn/dn`, `-cs`/`-cos` regex | Root domain, exact host, or keyword scope plus include/exclude patterns | The planned `/crawl` bounds; `map` already has host and path scoping |
| `-s depth-first/breadth-first`, priority queue | Visit order | Email crawling already prioritises contact and team pages; reuse the queue for `/crawl` |
| `-fsu` filter similar URLs, `-fst` threshold | Treats a path position as a parameter after N distinct values (`/users/123`, `/users/456`) | Stops crawls wasting budget on listing and detail pages with the same template |
| `-iqp` ignore query params | Visits a path once regardless of query values | Same purpose, simpler rule |
| `-pcs` page content similarity (simhash, TF-IDF, BM25) with a per-cluster budget | Skips pages whose normalised content is near-identical to one already processed | Saves browser renders on template clones; Katana reports about 70% filtered on its example |
| `-mdp` max pages per domain, `-hrl` per-host rate limit | Bounds per host | Needed for bulk email crawls and `/crawl` politeness |
| `-filter-page-type` error, captcha, parked | ML page-type classification | Mark parked and error pages `dead` instead of escalating them to the browser |

## Suggested order

1. **Escalation fixes from the [HTTP coverage benchmark](browser-routing.md#http-coverage-benchmark)** (no Katana code): about 9% of URLs reached the browser without a browser being able to help.
2. **jsluice in discovery** to find endpoints in the page's JavaScript, bounded by size.
3. **URL-shape and content-similarity filters** for crawl and email budgets.
4. **Endpoint classification rules** for GraphQL and SOAP, compared with Katana's knowledge base.
5. **URL-change poll** in the service's quiet wait for client-side routed apps.
6. **Recorded login flows** for profiles.
