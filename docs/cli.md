# CLI reference

```text
pagelode [--json] <URL>
pagelode extract [--json] <URL>
pagelode extract batch import [--database <postgres-url>] <file|->
pagelode extract batch run [--http-workers 1000] [--browser-workers -1] [--out data/pages] [--until-empty] [--progress-interval 10s]
pagelode extract batch status | retry [--state failed|dead]
pagelode map [--limit 500] [--max-duration 30s] [--depth 2] [--render never] [--explore] [--include <path>] [--exclude <path>] [--subdomains] [--json] <URL>
pagelode map endpoints [--wait-ms 1500] [--profile <name>] [--stealth] [--verbose] <URL>
pagelode map endpoints --har <capture.har> [<URL>]
pagelode emails [--max-pages 20] [--max-emails 100] [--max-duration 30s] [--render auto] [--profile <name>] [--verbose] <URL>
pagelode emails --input <file|-> [--database <postgres-url>] [--workers 32] [--max-pages 8] [--max-emails 50] [--max-duration 20s] [--render never]
pagelode profile login <name> <URL>
pagelode serve
pagelode version
pagelode help [command [subcommand]]
```

Put options before the URL. A bare domain such as `example.com` is accepted wherever a URL is. Every command reads its settings from environment variables; see [configuration](configuration.md) for proxies, browser mode, CapSolver, limits, and the Postgres database. Without `PAGELODE_DATABASE_URL`, learned routes and clearances last only for one process.

## Built-in help

```sh
pagelode help
pagelode help map
pagelode map endpoints --help
```

`pagelode help` lists every command; `pagelode help <command>` and `pagelode <command> --help` print that command's description, usage, subcommands, examples, and every flag with its type and default. Help never loads configuration or contacts the browser service. `pagelode --version` prints the version.

## `pagelode extract`

Loads one page through the waterfall and prints clean Markdown. `pagelode <URL>` is shorthand for `pagelode extract <URL>`.

| Option | Default | Effect |
|---|---|---|
| `--json`, `-j` | off | Print the complete result: content, title, links, provider, and every loader attempt |

### `pagelode extract batch`

Extracts millions of URLs: queues them as River jobs in Postgres (`PAGELODE_DATABASE_URL` or `--database`) and scrapes them with any number of `extract batch run` processes. Each URL is loaded over HTTP first; only pages that need a browser go to the browser stage, which spreads renders over every `PAGELODE_BROWSER_URL` node. A full or unreachable browser snoozes the job without using an attempt. Every subcommand fails without a database URL.

| Subcommand | Effect |
|---|---|
| `import <file\|->` | Add URLs or domains, one per line; URLs already queued or finished are counted as duplicates |
| `run` | Scrape until interrupted, or until no job is left with `--until-empty` |
| `status` | Count URLs per stage (`http`, `browser`) and state (`pending`, `running`, `retrying`, `done`, `dead`, `failed`) |
| `retry` | Queue every `failed` (default) or `dead` (`--state dead`) URL again in its own stage |

See [batch scraping](batch.md) for `run` flags and the output files.

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

### `pagelode map endpoints`

Captures one page's network traffic in the browser service and prints a JSON report of its data endpoints. See [endpoint discovery](discovery.md) for the report format.

| Option | Default | Effect |
|---|---|---|
| `--wait-ms` | `1500` | Longest wait for network activity after page load, 100–10000 ms; ends earlier when the page goes quiet |
| `--profile` | none | Use a saved signed-in browser profile |
| `--stealth` | off | Refuse to run unless `PAGELODE_PROXY_URL` is set. Every capture already uses the Patchright browser service |
| `--verbose` | off | Include request evidence, matching scores, and loading details |
| `--har` | none | Analyze a HAR file instead of opening a browser. An optional URL names the page it came from |

## `pagelode emails`

Crawls one site and prints one published email address per line. With `--input`, it crawls a list of sites in parallel and writes the results to Postgres instead. See [email finding](emails.md) for crawl behavior and the report format.

| Option | Default | `--input` default | Effect |
|---|---|---|---|
| `--input` | none | | File with one URL or domain per line, or `-` for standard input. Invalid and duplicate lines are skipped |
| `--database` | | `PAGELODE_DATABASE_URL` | Postgres URL that stores the results; required with `--input`, which fails without it |
| `--workers` | | `32` | Sites crawled in parallel, 1–512; needs `--input` |
| `--max-pages` | `20` | `8` | Pages to visit per site, 1–100 |
| `--max-emails` | `100` | `50` | Stop a site after this many unique addresses, 1–1000 |
| `--max-duration` | `30s` | `20s` | Crawl time budget per site, 1s–2m |
| `--render` | `auto` | `never` | `auto` uses a browser only when needed, `never` stays on HTTP, `always` renders every page |
| `--profile` | none | not allowed | Use a saved signed-in browser profile; every page loads in Patchright |
| `--verbose` | off | not allowed | Print the full JSON report: sources, page outcomes, and crawl details |

With `--input`, the `pagelode.email_sites` table holds one row per crawled site with its outcome, page counts, duration, any error, and when it was scraped, and the `pagelode.email_addresses` table holds one row per address and source page. Sites already in `email_sites` are skipped, so interrupting with Ctrl-C and rerunning resumes. Delete a site's row from `email_sites` (its addresses go with it) to crawl it again.

## `pagelode profile login <name> <URL>`

Opens a visible browser session on the browser service at the URL and prints its CDP URL. The session lasts up to one hour. Sign in in that window, then press Enter in the terminal to save the profile under `<name>` and close the session. Use it later with `--profile <name>` on `map endpoints` or `emails`. The browser service stores profiles in `BROWSER_SERVICE_PROFILES_DIR`.

```sh
pagelode profile login account https://example.com/login
pagelode map endpoints --profile account example.com
pagelode emails --profile account https://example.com/directory
```

## `pagelode serve`

Starts the HTTP API on `PORT` (default `8083`). See [HTTP API](api.md).

## Exit status

`0` on success. `1` on any error, including a failed extraction or an endpoint capture whose outcome is not `ok`. The JSON output is still printed before a failed `--json` extraction or endpoint capture exits. `emails` exits `0` on a partial crawl and `1` only when every page failed. `map` exits `1` only when it found no URL and could not load the site.
