# CLI reference

```text
pagelode [--json] <URL>
pagelode extract [--json] <URL>
pagelode discover [--wait-ms 1500] [--profile <name>] [--stealth] [--verbose] <URL>
pagelode discover --har <capture.har> [<URL>]
pagelode map [--limit 500] [--max-duration 30s] [--depth 2] [--render never] [--explore] [--include <path>] [--exclude <path>] [--subdomains] [--json] <URL>
pagelode emails [--max-pages 20] [--max-emails 100] [--max-duration 30s] [--render auto] [--profile <name>] [--verbose] <URL>
pagelode emails-bulk [--db data/emails.sqlite] [--workers 32] [--max-pages 8] [--max-emails 50] [--max-duration 20s] [--render never] <file|->
pagelode batch import [--queue <postgres-url>] <file|->
pagelode batch run [--http-workers 100] [--browser-workers -1] [--out data/pages] [--until-empty]
pagelode batch status | retry [--state failed] [--to pending]
pagelode profile login <name> <URL>
pagelode serve
pagelode version
pagelode help
```

Put options before the URL. A bare domain such as `example.com` is accepted wherever a URL is. Every command reads its settings from environment variables; see [configuration](configuration.md) for proxies, browser mode, CapSolver, and limits.

## Built-in help

```sh
pagelode help
pagelode help map
pagelode map --help
```

`pagelode help` lists every command; `pagelode help <command>` and `pagelode <command> --help` print that command's description, usage, examples, and every flag with its type and default. Help never loads configuration or contacts the browser service. `pagelode --version` prints the version.

## `pagelode <URL>` / `pagelode extract <URL>`

Loads one page through the waterfall and prints clean Markdown.

| Option | Default | Effect |
|---|---|---|
| `--json`, `-j` | off | Print the complete result: content, title, links, provider, and every loader attempt |

## `pagelode discover`

Captures a page's network traffic and prints a JSON report of its data endpoints. See [endpoint discovery](discovery.md) for the report format.

| Option | Default | Effect |
|---|---|---|
| `--wait-ms` | `1500` | Longest wait for network activity after page load, 100–10000 ms; ends earlier when the page goes quiet |
| `--profile` | none | Use a saved signed-in browser profile |
| `--stealth` | off | Refuse to run unless `PAGELODE_PROXY_URL` is set. Every capture already uses the Patchright browser service |
| `--verbose` | off | Include request evidence, matching scores, and loading details |
| `--har` | none | Analyze a HAR file instead of opening a browser. An optional URL names the page it came from |

## `pagelode map`

Lists the page URLs of one site, one per line. See [site mapping](map.md) for crawl behavior and the JSON report.

| Option | Default | Effect |
|---|---|---|
| `--limit` | `500` | Maximum URLs, 1–5000 |
| `--max-duration` | `30s` | Time budget, 1s–2m |
| `--include` | all | Keep only URLs whose path matches this pattern, such as `/blog/*`; `*` matches any characters. Repeatable |
| `--exclude` | none | Drop URLs whose path matches this pattern. Wins over `--include`. Repeatable |
| `--subdomains` | off | Also include subdomains of the site |
| `--depth` | `2` | Link levels to follow from the start page, 1–5 |
| `--render` | `never` | `never`, `auto`, or `always`: when pages load in the browser service |
| `--explore` | off | Scroll and click safe controls on the start page to reveal links; needs `--render auto` or `always` |
| `--json` | off | Print the report: each URL with its `source` (`sitemap`, `page`, `link`, `rendered`, or `explore`) and `lastModified`, plus `endpoints`, `templates`, crawl counts, and warnings |

## `pagelode emails`

Crawls a site and prints one published email address per line. See [email finding](emails.md) for crawl behavior and the report format.

| Option | Default | Effect |
|---|---|---|
| `--max-pages` | `20` | Pages to visit, 1–100 |
| `--max-emails` | `100` | Stop after this many unique addresses, 1–1000 |
| `--max-duration` | `30s` | Crawl time budget, 1s–2m |
| `--render` | `auto` | `auto` uses a browser only when needed, `never` stays on HTTP, `always` renders every page |
| `--profile` | none | Use a saved signed-in browser profile; every page loads in Patchright |
| `--verbose` | off | Print the full JSON report: sources, page outcomes, and crawl details |

## `pagelode emails-bulk`

Crawls many sites in parallel and writes the results to SQLite. The input is a file with one URL or domain per line, or `-` for standard input. Invalid and duplicate lines are skipped.

| Option | Default | Effect |
|---|---|---|
| `--db` | `data/emails.sqlite` | SQLite file to create or extend |
| `--workers` | `32` | Sites crawled in parallel, 1–512 |
| `--max-pages` | `8` | Pages to visit per site, 1–100 |
| `--max-emails` | `50` | Stop a site after this many unique addresses, 1–1000 |
| `--max-duration` | `20s` | Crawl time budget per site, 1s–2m |
| `--render` | `never` | `auto`, `never`, or `always`; `never` stays on fast HTTP |

The `sites` table holds one row per crawled site with its outcome, page counts, duration, and any error. The `emails` table holds one row per address and source page. Sites already in `sites` are skipped, so interrupting with Ctrl-C and rerunning resumes. Delete the database to start over.

## `pagelode batch`

Queues URLs in Postgres (`PAGELODE_QUEUE_URL` or `--queue`) and scrapes them with any number of `batch run` processes. Each URL is loaded over HTTP first; only pages that need a browser go to the browser stage, which spreads renders over every `PAGELODE_BROWSER_URL` node. A full or unreachable browser puts the URL back without using an attempt. See [batch scraping](batch.md) for flags and output.

## `pagelode profile login <name> <URL>`

Opens a visible browser session on the browser service at the URL and prints its CDP URL. The session lasts up to one hour. Sign in in that window, then press Enter in the terminal to save the profile under `<name>` and close the session. Use it later with `--profile <name>` on `discover` or `emails`. The browser service stores profiles in `BROWSER_SERVICE_PROFILES_DIR`.

```sh
pagelode profile login account https://example.com/login
pagelode discover --profile account example.com
pagelode emails --profile account https://example.com/directory
```

## `pagelode serve`

Starts the HTTP API on `PORT` (default `8083`). See [HTTP API](api.md).

## Exit status

`0` on success. `1` on any error, including a failed extraction or a discovery whose outcome is not `ok`. The JSON output is still printed before a failed `--json` extraction or discovery exits. `emails` exits `0` on a partial crawl and `1` only when every page failed. `map` exits `1` only when it found no URL and could not load the site.
