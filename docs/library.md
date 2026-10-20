# Using PageLode as a library

The packages under `pkg/` are importable; `internal/` holds the app's CLI, HTTP API, config, and Postgres store.

| Package | Contents |
|---|---|
| `fetch` | Loader-neutral documents, sessions, and captures; the profiled HTTP loader (`NewHTTPClient`); classification; the HTTP-then-browser waterfall with per-host routes and clearances; HTML-to-Markdown extraction (`ExtractHTML`); the bounded concurrency `Limiter` |
| `browser` | Client for the Bun browser service, which must be running from `browser-service/`; identities, proxy URL templates, the challenge `Solver`, and the CapSolver client |
| `sitemap` | Site mapping, script endpoint scanning, simhash near-duplicate detection, URL shapes and templates |
| `discovery` | Data-endpoint discovery from browser captures and HAR files |
| `emails` | Email-finding crawl |
| `batch` | Two-stage bulk extraction on River job queues that writes rolling JSONL.gz files |

```sh
go get github.com/simonbalfe/pagelode
```

```go
package main

import (
	"context"
	"fmt"

	"github.com/simonbalfe/pagelode/pkg/fetch"
)

func main() {
	client := fetch.NewHTTPClient("")
	doc, err := client.Fetch(context.Background(), "https://example.com", fetch.Session{})
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.StatusCode, doc.Title)
}
```
