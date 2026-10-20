# Browser stage performance

Notes from the 10,000-URL Common Crawl run on 20–21 October 2026 (`data/commoncrawl.duckdb`), an Apple M4 Pro laptop with 12 cores and 24 GB of RAM, one home IP, no proxy, headless Chrome. About 13% of URLs (1,328) needed the browser stage; the rest finished over HTTP.

## What was slow

| Cause | Evidence | Fix |
|---|---|---|
| One identity Chrome closed under every render whenever any render passed its deadline, then relaunched on a locked profile | `ProcessSingleton` and `context closed` errors; renders hung with `active 4, waiting 4`; 0 browser pages completed for minutes | The pool now aborts only the late render's page, launches once per profile, and fails waiting renders quickly when a launch fails; see [browser service](browser-service.md) |
| Renders that never finish held a tab until the page timeout | Renders that hit a 20 s timeout used 2,921 tab-seconds; all 597 successful renders used 1,723. Successful renders: 2.3 s median, 6.2 s p95, 11.4 s p99 | `--page-timeout` defaults to 10 s |
| Failed renders were retried | The same deterministic failures (blocks, empty pages) rendered up to three times | Browser jobs get one attempt; only `429` and unreachable nodes snooze without using one |
| Pages Chrome had rendered were marked "needs JavaScript" | `noscript requires JavaScript` and `scripts with little visible content` on rendered HTML | `fetch.Classify` skips the render heuristics for Patchright documents |
| PDFs and downloads went to the browser | 124 hand-offs; Chrome fails them with `Download is starting` | Non-HTML responses that cannot be extracted fail in the HTTP stage |
| 4 renders per node, one Chrome profile | Laptop about half idle at 16 renders | Defaults are 16 renders per node and 4 identities |

## Measurements

Same 240 browser-stage URLs, sent straight to `/v1/render`:

| Setup | Pages/s | Successful render p50 / p90 | CPU busy | Chrome RSS |
|---|---|---|---|---|
| 16 tabs, 20 s timeout, all resources | 2.50 | 1.94 s / 4.59 s | ~6 of 12 cores | |
| 16 tabs, 20 s timeout, images, media, and fonts blocked | 2.58 | 1.66 s / 3.43 s | ~6 of 12 cores | |
| 16 tabs, 10 s timeout, blocked | 3.13 | 1.89 s / — | 5.7 cores | 10.3 GB |
| 32 tabs, 10 s timeout, blocked | 4.79 | 2.48 s / — | 6.6 cores | 15 GB |

Blocking resources saves bytes, not time: it moved throughput by 3%. Rendering costs about 1 core-second per page; at 32 tabs memory, not CPU, is the limit on 24 GB. Summed RSS over-counts memory shared between Chrome processes, so the real cost per tab is lower than 0.47 GB.

The HTTP stage is separate: 1,000 HTTP workers loaded about 130 URLs/s on the same laptop against about 40 with 100 workers, then the CPU was the limit.

## Capacity estimate for 500 pages a second

Extrapolated from the measurements above, not tested on server hardware:

| Target | Chrome tabs at once | CPU | RAM |
|---|---|---|---|
| 500 browser renders a second | ~2,250 | ~500 M4 cores, about 1,000 server vCPU | 0.7–1 TB at the RSS figure above |
| 500 pages a second through the pipeline (HTTP first, ~14% to the browser) | ~320 | ~70 M4 cores plus ~4 for HTTP | 100–150 GB |

Scale out by pointing `PAGELODE_BROWSER_URL` at several browser nodes and running several `extract batch run` processes on one queue. IP blocking becomes the limit before hardware does: about a third of browser failures were 403, Akamai, or DataDome responses from a single home IP.

## Lightpanda evaluation

[Lightpanda](https://lightpanda.io/docs/) 1.0.1 is a headless browser written in Zig that speaks CDP. All 1,328 browser-stage URLs from the run went through `lightpanda fetch --dump html --wait-until networkalmostidle --wait-ms 8000 --terminate-ms 10000` with 25 processes, and the HTML was judged with PageLode's own `fetch.Classify` and `fetch.ExtractHTML`, so "ok" means the same for both browsers.

| Why HTTP handed the URL to the browser | URLs | Chrome ok | Lightpanda ok | Either ok | Lightpanda median |
|---|---|---|---|---|---|
| Bot-blocked (403/503, Cloudflare, Akamai, access denied) | 252 | 34 (13%) | 38 (15%) | 56 (22%) | 0.74 s |
| JavaScript shell | 583 | 465 (79%) | 458 (78%) | 509 (87%) | 2.0 s |
| Other (non-HTML, HTTP errors, remembered browser route) | 493 | 98 (19%) | 76 (15%) | 114 (23%) | 1.0 s |
| All | 1,328 | 597 | 572 | 679 | |

| Cost | Lightpanda, 25 processes | Chrome, 32 tabs |
|---|---|---|
| Throughput | 10.9 URLs/s | 4.8 URLs/s |
| Memory | ~54 MB per process, 3.5 GB peak | ~0.47 GB per tab (summed RSS) |

- Neither browser gets past bot walls without proxies. Lightpanda reports most of them itself (`BotChallenge`) in under a second, where Chrome waits out its timeout.
- On JavaScript shells Lightpanda matches Chrome, and each rescues pages the other misses.
- Chrome rescued none of Lightpanda's DNS, connection, or timeout failures, 22% of its "no readable content" and `BotChallenge` failures, 13% of its blocks, and 8% of its certificate failures.

Proposed tier: HTTP, then Lightpanda, then Chrome only for Lightpanda's no-content, blocked, `BotChallenge`, and certificate failures. On this sample that extracts ~677 pages instead of 597 and cuts Chrome renders from 1,328 to ~590.

Caveats: the Chrome results come from the batch run, which had a retry pass and ran earlier; Lightpanda had one try. One sample of Common Crawl URLs.

## Sources

- [Lightpanda real-world benchmark](https://lightpanda.io/blog/posts/from-local-to-real-world-benchmarks): 25 parallel tasks on 933 JavaScript pages, 123 MB and 5 s against Chrome's 2 GB and 46 s; Chrome stopped improving past about 5 tabs. Vendor benchmark.
- [Lightpanda bot detection issues #990](https://github.com/lightpanda-io/browser/issues/990) and [#1177](https://github.com/lightpanda-io/browser/issues/1177): no stealth layer.
- [headlessperfbench](https://github.com/atom41research/headlessperfbench): `chrome-headless-shell` uses about half the memory of headless Chrome but serves blank or skeleton pages on React and Next.js sites.
- [Detecting Bot Detection (University of Bamberg, 2026)](https://arxiv.org/html/2606.14525v1): headless Chromium soft-blocked on 15% of 10,000 sites against 7% for other setups; 75% of headless-only blocks came from headers alone.
- [Blocking resources in Playwright, measured](https://www.scrapeless.com/en/blog/block-resources-playwright-scraping-browser): blocking images, media, and fonts cut transfer 67%, ~25%, and ~0% on three pages; time moved both ways.
- [Chromium process model and site isolation](https://chromium.googlesource.com/chromium/src/+/refs/tags/150.0.7871.124/docs/process_model_and_site_isolation.md): a renderer process per site, including cross-site iframes.
- [Headless Chromium at scale (Rendershot)](https://dev.to/rendershot/headless-chromium-at-scale-four-fixes-for-a-fleet-that-kept-eating-ram-1mdp): one browser per worker, a page cap matched to the queue, scheduled restarts.

## Next steps

1. Add the Lightpanda tier to `extract batch` and the browser container image.
2. Proxies and headed Chrome for bot-blocked hosts.
3. Fewer renderer processes per tab: block ad and tracker iframes, and measure site-isolation flags.
4. Finish a render once its text stops growing instead of waiting for the network to go quiet.
5. Restart each identity's Chrome on a schedule to bound memory growth.
