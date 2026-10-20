# HTTP API

`pagelode serve` starts the API on `PORT` (default `8083`).

```sh
curl -sS http://localhost:8083/extract \
  -H 'content-type: application/json' \
  -d '{"url":"https://example.com"}'
```

The `/extract` response contains:

- `content`: the page as clean Markdown
- `title`: the page title
- `links`: same-site links found on the page
- `provider`: the loader that succeeded
- `attempts`: what PageLode tried and why it escalated

The API serves the same operations as the CLI:

| Endpoint | CLI equivalent | Body |
|---|---|---|
| `POST /extract` | `pagelode <URL>` | `{"url"}` |
| `POST /map` | `pagelode map` | `{"url", "limit", "maxDurationMs", "subdomains", "include", "exclude", "depth", "render", "explore"}` |
| `POST /discover` | `pagelode discover` | `{"url", "waitMs", "profile", "stealth", "verbose"}` or `{"har"}` for an offline capture |
| `POST /emails` | `pagelode emails` | `{"url", "maxPages", "maxEmails", "maxDurationMs", "profile", "render"}` |
| `GET /healthz` | none | none |

`emails-bulk`, `batch`, `profile login`, and `version` have no API form. Request fields, status codes, and response formats are described in [site mapping](map.md), [endpoint discovery](discovery.md), and [email finding](emails.md).
