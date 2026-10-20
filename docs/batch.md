# Batch scraping

`pagelode extract batch` scrapes millions of URLs through a [River](https://riverqueue.com) job queue in Postgres. Any number of `extract batch run` processes, on any number of machines, can work on the same queue at once.

```sh
export PAGELODE_DATABASE_URL=postgres://user:pass@host/db
pagelode extract batch import urls.txt
PAGELODE_BROWSER_URL=http://b1:8090,http://b2:8090 PAGELODE_BROWSER_IDENTITIES=8 \
  pagelode extract batch run --http-workers 400 --out /data/pages --until-empty
pagelode extract batch status
pagelode extract batch retry --state failed
```

## Flow

```mermaid
flowchart LR
    Import[batch import] --> HTTPJob[(extract_http job, queue http)]
    HTTPJob --> HTTP[HTTP stage: replay, tls-client]
    HTTP -->|usable| Writer[JSONL.gz writer]
    HTTP -->|404/410| Dead[(dead: cancelled)]
    HTTP -->|other failure| Failed[(failed: discarded)]
    HTTP -->|JS shell, blocked, remembered browser route| BrowserJob[(extract_browser job, queue browser)]
    BrowserJob --> Browser[Browser stage: Patchright, then CapSolver]
    Browser -->|usable| Writer
    Browser -->|no capacity, host busy| BrowserJob
    Browser -->|404/410| Dead
    Browser -->|failed| Failed
    Writer -->|file closed and renamed| Done[(done: completed)]
```

- **Queue.** River's tables live in the `pagelode` schema. Every `extract batch` command creates or upgrades them on start with River's own migrations. Each URL becomes an `extract_http` job on the `http` queue. Pages that need a browser get an `extract_browser` job on the `browser` queue. Jobs are unique per kind by URL in every state, so `import` skips URLs that are already queued or finished and counts them as duplicates.
- **HTTP stage.** `fetch.Service.ExtractHTTP` runs clearance replay and tls-client only. A page that needs a browser gets its browser job in the same transaction that completes the HTTP job. A non-HTML response that cannot be extracted (a PDF or download) fails here instead, because a browser cannot extract it either. HTTP jobs get one attempt: a failure other than 404/410 discards the job straight away.
- **Browser stage.** `fetch.Service.ExtractBrowser` runs Patchright, then CapSolver. Only capacity is retried: when every browser node is full (`429`) or unreachable, the job is snoozed for about 5 s, which does not use an attempt. A failed render, including one that passes `--page-timeout`, is `failed` at once with no retry; `retry --state failed` queues those again.
- **Hosts.** A process loads at most `--host-concurrency` pages of one host at once. A job whose host is busy is snoozed for about 1 s without using an attempt.
- **Output.** Successful pages are appended to `<out>/pages-<owner>-<time>-<seq>.jsonl.gz`, one `batch.Record` per line (the `/extract` result plus the River job `id` and `fetchedAt`). A file is written as `.part`, synced, and renamed after 5,000 pages, after 2 s, or once no page has arrived for 100 ms. A job completes only after its file has been renamed. The job's `output` (River metadata) then holds `contentRef` = `<file>:<line>` with the provider and status.
- **Crashes.** A crash loses at most the open file. Its jobs stay `running` until River's rescuer finds them after `--lease`. A rescued job counts the lost run as its one attempt, so it is discarded and shows as `failed`. `retry --state failed` queues them again.
- **Retention.** Finished jobs are never deleted (`CompletedJobRetentionPeriod`, `CancelledJobRetentionPeriod`, and `DiscardedJobRetentionPeriod` are `-1`). They back deduplication, `status`, and `retry`.
- **Shutdown.** `SIGINT`/`SIGTERM` stops fetching jobs and cancels pages in flight. The running jobs are snoozed so they can be picked up again immediately, without using an attempt.

## Status and retry

`status` prints the number of URLs per stage (`http`, `browser`) and state:

| State | River state |
|---|---|
| `pending` | `available`, `scheduled` (including snoozed jobs), `pending` |
| `running` | `running` |
| `retrying` | `retryable` |
| `done` | `completed`. In the `http` stage this includes URLs passed to the browser stage |
| `dead` | `cancelled` (404 or 410) |
| `failed` | `discarded` |

`retry --state failed` (the default) or `retry --state dead` makes every job in that state available again in its own stage through River's `JobRetry`. A job that had used all its attempts gets one more.

## Browser capacity

`--browser-workers` defaults to every browser slot: the number of `PAGELODE_BROWSER_URL` nodes × `PAGELODE_BROWSER_NODE_SLOTS`. Set `PAGELODE_BROWSER_NODE_SLOTS` to each node's `BROWSER_SERVICE_CONCURRENCY`, so Go never sends a node more renders than it runs at once. Hosts map to `PAGELODE_BROWSER_IDENTITIES` identity slots by hash; each slot has its own proxy session, warm Chrome context, and rotation, and lives on node `slot % nodes`. A node that refuses connections is skipped for 15 s and its renders go to the next node. See [configuration](configuration.md).

## Flags (`extract batch run`)

| Flag | Default | Effect |
|---|---|---|
| `--database` | `PAGELODE_DATABASE_URL` | Postgres URL of the queue; every subcommand fails without it |
| `--out` | `data/pages` | Directory for page files |
| `--http-workers` | `1000` | River workers on the `http` queue. On an M4 Pro laptop, 1,000 workers loaded ~130 URLs/s against ~40 URLs/s with 100; past that the CPU (TLS handshakes and extraction) is the limit, so scale out with more processes or machines on the same queue |
| `--browser-workers` | `-1` (all slots) | River workers on the `browser` queue; `0` skips the browser stage |
| `--until-empty` | off | Exit when no job of a worked stage is pending, running, or retrying |
| `--host-concurrency` | `4` | Pages of one host loaded at once by this process |
| `--lease` | `10m` | River's `RescueStuckJobsAfter`: how long a job may stay `running` before any process works it again; must exceed `--page-timeout` + 4 s |
| `--page-timeout` | `10s` | Time budget for loading one URL in one stage; a page that takes longer fails without a retry. On the Common Crawl sample successful renders took 2.3 s median, 6.2 s at p95, and 11.4 s at p99, while renders that hit a 20 s timeout used more browser time than every successful render combined. River's job timeout is this plus 4 s for writing the page |

## Reading results

```sh
duckdb -c "select url, title, provider, length(content) from read_json_auto('/data/pages/*.jsonl.gz', format='newline_delimited') limit 10"
```

`content` is capped at 12,000 characters, as in `/extract`.

## Limits

- Per-host limits apply per process; several processes can each load `--host-concurrency` pages of one host.
- A worker waits up to 2 s after loading a page until the page's file is renamed, so each worker loads fewer pages per second than it could. Add workers to compensate.
- Learned routes and clearances are stored in `PAGELODE_DATABASE_URL` and loaded when a process starts, so a process learns a protected host once and later processes reuse it.
- HTTP uses the proxy session of the host's identity slot, so HTTP and browser renders of one host share an exit IP.

## Measured

These numbers were measured on the previous hand-written queue (`pagelode.urls`, replaced by River). They have not been remeasured on River.

20 October 2026, Apple M4 Pro behind a VPN (exit IP in Oslo on AS9009 M247, a datacenter range), no proxy, Postgres 17 in Docker, two headless browser nodes with 3 slots each, 4 identities:

| Run | Result |
|---|---|
| 405 Common Crawl URLs, 50 HTTP workers | HTTP stage ~23 pages/s; 299 done over HTTP, 6 dead, 100 sent to the browser, 23 of those done (headless, no proxy) |
| Browser node killed mid-run | Renders moved to the other node; no URL failed |
| Both nodes killed, then restarted | 368 URLs put back, attempts unchanged; all finished after restart |
| `SIGINT` mid-run | Exited in 0.1 s; 0 URLs left leased |
| Two processes on one queue, 578 URLs | 474 done, 8 dead, 96 failed; every URL processed once, no duplicate records |

17 of the browser failures were 45 s page-load timeouts. 14 of those sites drop TCP connections from the M247 exit IP but accept them from other countries (checked with check-host.net); they sit on shared hosting such as Host Europe, All-Inkl, GoDaddy, and DreamHost. Of the other 3, one site was down everywhere, one accepted connections only from Russia, and one sent a body labelled gzip that is not valid gzip. All 17 had already failed over HTTP before they reached the browser.
