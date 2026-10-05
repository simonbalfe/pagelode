# Email finding

`pagelode emails` and `POST /emails` search a website for published email addresses. The CLI prints unique addresses. The API and verbose CLI output include source URLs, discovery methods, page outcomes, and crawl coverage.

## CLI

```sh
pagelode emails example.com
pagelode emails --verbose example.com
pagelode emails --max-pages 10 --max-emails 50 --max-duration 15s example.com
pagelode emails --render never example.com
pagelode emails --render always https://example.com/team
pagelode emails --profile account https://app.example.com
```

Place options before the URL. By default, output contains one email address per line and is empty when no addresses are found. Add `--verbose` for the complete JSON report, including sources, page outcomes, warnings, and crawl statistics. A partial crawl prints its collected addresses and exits successfully; a crawl where every page failed exits with an error.

## API

```sh
curl -sS http://localhost:8083/emails \
  -H 'content-type: application/json' \
  -d '{"url":"example.com","maxPages":20,"maxEmails":100,"maxDurationMs":30000,"render":"auto"}'
```

| Field | Default | Accepted values |
|---|---|---|
| `url` | Required | HTTP(S) URL or domain |
| `maxPages` | 20 | 1–100 |
| `maxEmails` | 100 | 1–1000 |
| `maxDurationMs` | 30000 | 1000–120000 |
| `render` | `auto` | `auto`, `never`, `always` |
| `profile` | None | Existing saved profile name |

Example address entry:

```json
{
  "address": "contact@example.com",
  "sources": [
    {"url": "https://example.com/contact", "foundIn": "mailto"},
    {"url": "https://example.com/contact", "foundIn": "text"}
  ]
}
```

`foundIn` is `mailto`, `text`, `structured_data`, `decoded`, or `network_json`. Network JSON sources identify the response URL. All URL query values are redacted in reports. Raw captures and credentials are omitted.

`summary` contains `pagesVisited`, `pagesFailed`, `emailsFound`, `durationMs`, and `limited`. `pagesVisited` counts page attempts, including failures. `outcome` is `ok`, `partial` when a limit or failed page reduced coverage, or `failed` when every attempted page failed. Finding zero addresses can still be a successful crawl. Invalid API requests return 400, queue saturation returns 429, and the server's request deadline returns 408. The crawler's own duration limit returns its report with a warning.

## Crawl behavior

The crawler starts with the supplied page and prioritizes contact, team, staff, directory, people, about, location, and support links. Anonymous searches also inspect `/sitemap.xml` and bounded sitemap indexes for those page paths. Other same-site links can fill the remaining page budget.

Scope includes the starting hostname and its equivalent `www` hostname on the same explicit port. Other subdomains and domains are excluded. Links to common assets and paths containing logout, unsubscribe, delete, or remove actions are skipped. Redirects outside scope are reported as failed pages. Tracking query parameters and fragments are removed from discovered links.

Anonymous page loads use up to four concurrent workers within the service's shared limits. Each load has a ten-second timeout. Crawls retain at most 1,000 candidate URLs, follow links up to depth two, and fetch at most four sitemap documents. Sitemap fetches use HTTP and do not count toward `maxPages`. Limits produce warnings when reached. The service request timeout can shorten the crawl duration.

The extractor reads full HTML before Markdown cleanup, preserving contact details in headers and footers. It handles multiple and percent-encoded mailto recipients, visible text, email fields in JSON-LD and embedded JSON, explicit `[at]`/`(at)` and `[dot]`/`(dot)` obfuscation, and Cloudflare's encoded email attributes and links. Captured successful JSON responses are searched for email fields. Script code and elements with `hidden` or `aria-hidden="true"` attributes are excluded. Addresses are lowercased, checked for syntax, sorted, and deduplicated while retaining their sources.

`auto` starts with HTTP and escalates JavaScript shells or pages with signals of dynamically loaded contact data. If HTTP already yields addresses, it keeps that fast result. `always` captures browser-rendered pages even when HTTP would find addresses; use it to inspect additional JavaScript-loaded contacts. `never` stays on HTTP. Browser captures observe traffic for 500 ms after loading, so later interactions and delayed responses can require future readiness controls.

## Authenticated searches

```sh
pagelode profile login account https://example.com/login
pagelode emails --profile account https://example.com/directory
```

Sign in using the opened browser and press Enter in the terminal to save the profile. Email searches with that profile use Patchright for every page and load serially to avoid concurrent access to browser storage. They skip anonymous sitemap probing. `render: "never"` cannot be combined with a profile. Profile storage and login behavior are described in [endpoint discovery](discovery.md).

Address syntax and presence establish that an address was published; they do not verify deliverability. The tool discovers addresses without guessing name-based patterns or sending mail. Addresses using a different email domain from the page remain eligible.

## Code organization

`internal/emails` owns request validation, page loading policy, candidate selection, address decoding, and bounded crawling. The CLI and API share its service. HTTP, Chromedp, and Patchright provide the existing acquisition and capture implementations; the browser worker contains no email extraction logic.
