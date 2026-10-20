# Batch scraping

`pagelode extract batch` scrapes millions of URLs from one shared Postgres queue. Any number of `extract batch run` processes, on any number of machines, can work on the same queue at once.

```sh
export PAGELODE_QUEUE_URL=postgres://user:pass@host/db
pagelode extract batch import urls.txt
PAGELODE_BROWSER_URL=http://b1:8090,http://b2:8090 PAGELODE_BROWSER_IDENTITIES=8 \
  pagelode extract batch run --http-workers 400 --out /data/pages --until-empty
pagelode extract batch status
pagelode extract batch retry --state failed --to pending
```

## Flow

```mermaid
flowchart LR
    Import[batch import] --> Pending[(pending)]
    Pending -->|claim + lease| HTTP[HTTP stage: replay, tls-client]
    HTTP -->|usable| Writer[JSONL.gz writer]
    HTTP -->|404/410| Dead[(dead)]
    HTTP -->|JS shell, blocked, remembered browser route| NeedsBrowser[(needs_browser)]
    NeedsBrowser -->|claim + lease| Browser[Browser stage: Patchright, then CapSolver]
    Browser -->|usable| Writer
    Browser -->|no capacity| NeedsBrowser
    Browser -->|failed, attempts left| NeedsBrowser
    Browser -->|failed, no attempts left| Failed[(failed)]
    Writer -->|file closed and renamed| Done[(done)]
```

- **Queue.** Table `pagelode.urls`, created on first use. `import` normalises each line (adds `https://` to bare domains, lowercases the host, drops the fragment) and ignores URLs already queued. `Claim` uses `FOR UPDATE SKIP LOCKED` and leases rows by moving `next_at` forward, so an expired lease makes a URL claimable again without any cleanup job.
- **HTTP stage.** `orchestrator.ExtractHTTP` runs clearance replay and tls-client only. Pages that need a browser are written back as `needs_browser`, not held in memory.
- **Browser stage.** `orchestrator.ExtractBrowser` runs Patchright, then CapSolver. When every browser node is full (`429`) or unreachable, the URL is put back after about 5 s without using an attempt. Other failures use an attempt and retry with exponential backoff from 1 minute until `--max-attempts`.
- **Output.** Successful pages are appended to `<out>/pages-<owner>-<time>-<seq>.jsonl.gz`, one `batch.Record` per line (the `/extract` result plus `id` and `fetchedAt`). A file is written as `.part`, synced, and renamed after 5,000 pages, 30 s, or when a stage runs out of work. Only then are its URLs marked `done`, with `content_ref` = `<file>:<line>`. A crash loses at most the open file; its URLs are scraped again once their leases expire.
- **Shutdown.** `SIGINT`/`SIGTERM` stops claiming, cancels pages in flight, and puts them back as claimable immediately without using an attempt.

## Browser capacity

`--browser-workers` defaults to every browser slot: the number of `PAGELODE_BROWSER_URL` nodes × `PAGELODE_BROWSER_NODE_SLOTS`. Set `PAGELODE_BROWSER_NODE_SLOTS` to each node's `BROWSER_SERVICE_CONCURRENCY`, so Go never sends a node more renders than it runs at once. Hosts map to `PAGELODE_BROWSER_IDENTITIES` identity slots by hash; each slot has its own proxy session, warm Chrome context, and rotation, and lives on node `slot % nodes`. A node that refuses connections is skipped for 15 s and its renders go to the next node. See [configuration](configuration.md).

## Flags (`extract batch run`)

| Flag | Default | Effect |
|---|---|---|
| `--queue` | `PAGELODE_QUEUE_URL` | Postgres URL of the queue |
| `--out` | `data/pages` | Directory for page files |
| `--http-workers` | `100` | URLs loaded over HTTP at once |
| `--browser-workers` | `-1` (all slots) | URLs rendered at once; `0` skips the browser stage |
| `--until-empty` | off | Exit when no `pending` or `needs_browser` URL is left |
| `--max-attempts` | `3` | Browser failures before a URL is `failed` |
| `--host-concurrency` | `4` | Pages of one host loaded at once by this process |
| `--lease` | `10m` | How long a claimed URL stays reserved; must exceed `--page-timeout` + 30 s |
| `--page-timeout` | `90s` | Time budget for one stage of one URL |

## Reading results

```sh
duckdb -c "select url, title, provider, length(content) from read_json_auto('/data/pages/*.jsonl.gz', format='newline_delimited') limit 10"
```

`content` is capped at 12,000 characters, as in `/extract`.

## Limits

- Per-host limits apply per process; several processes can each load `--host-concurrency` pages of one host.
- Learned routes and clearances stay in each machine's `PAGELODE_STATE_PATH`, so each machine learns a protected host once.
- HTTP uses the proxy session of the host's identity slot, so HTTP and browser renders of one host share an exit IP.

## Measured

20 October 2026, Apple M4 Pro behind a VPN (exit IP in Oslo on AS9009 M247, a datacenter range), no proxy, Postgres 17 in Docker, two headless browser nodes with 3 slots each, 4 identities:

| Run | Result |
|---|---|
| 405 Common Crawl URLs, 50 HTTP workers | HTTP stage ~23 pages/s; 299 done over HTTP, 6 dead, 100 sent to the browser, 23 of those done (headless, no proxy) |
| Browser node killed mid-run | Renders moved to the other node; no URL failed |
| Both nodes killed, then restarted | 368 URLs put back, attempts unchanged; all finished after restart |
| `SIGINT` mid-run | Exited in 0.1 s; 0 URLs left leased |
| Two processes on one queue, 578 URLs | 474 done, 8 dead, 96 failed; every URL processed once, no duplicate records |

17 of the browser failures were 45 s page-load timeouts. 14 of those sites drop TCP connections from the M247 exit IP but accept them from other countries (checked with check-host.net); they sit on shared hosting such as Host Europe, All-Inkl, GoDaddy, and DreamHost. Of the other 3, one site was down everywhere, one accepted connections only from Russia, and one sent a body labelled gzip that is not valid gzip. All 17 had already failed over HTTP before they reached the browser.
