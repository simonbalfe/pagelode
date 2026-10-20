# Using PageLode as a library

The packages under `pkg/` are importable; `internal/` holds the app's CLI, HTTP API, config, and SQLite stores.

- Shared types: `page` — loader-neutral documents, sessions, and captures.
- Loaders:
  - `httpfetch` — profiled direct HTTP loader.
  - `browser` — client for the Bun browser service; needs the service from `browser-service/` running.
  - `capsolver` — CapSolver task polling for Cloudflare, reCAPTCHA, and DataDome.
  - `captcha` — wraps the browser client to solve reported challenges and render again.
- Policy:
  - `classify` — block, render, retry, dead, and accept decisions.
  - `memory` — expiring per-host routes and clearances.
  - `proxy` — proxy URL templates and session substitution.
  - `limit` — bounded concurrency and queueing.
- Waterfall: `orchestrator` — HTTP-then-browser escalation with attempt evidence.
- Processors and jobs:
  - `extract` — Readability, Markdown, links, structured data.
  - `sitemap` — site mapping.
  - `emails` — email-finding crawl.
  - `discovery` — data-endpoint discovery from browser captures and HAR files.
  - `scriptscan` — script URL and endpoint extraction.
  - `similarity` — simhash near-duplicate detection.
  - `urlshape` — URL shapes and path templates.
  - `profile` — named profile validation.
- Bulk scraping:
  - `queue` — shared Postgres URL queue with leased claims.
  - `batch` — two-stage runner over `queue` that writes rolling JSONL.gz files.

```sh
go get github.com/simonbalfe/pagelode
```

```go
package main

import (
	"context"
	"fmt"

	"github.com/simonbalfe/pagelode/pkg/httpfetch"
	"github.com/simonbalfe/pagelode/pkg/page"
)

func main() {
	client := httpfetch.New("") // optional proxy URL
	doc, err := client.Fetch(context.Background(), "https://example.com", page.Session{})
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.StatusCode, doc.Title)
}
```
