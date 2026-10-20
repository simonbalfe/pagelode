# Site mapping

`pagelode map` and `POST /map` list the page URLs of one site. Flags are listed in the [CLI reference](cli.md#pagelode-map).

```sh
pagelode map example.com
pagelode map --depth 3 --limit 2000 example.com
pagelode map --include '/blog/*' --exclude '/blog/tag/*' example.com
pagelode map --render auto --explore https://app.example.com
pagelode map --json example.com
```

## How it finds URLs

It reads `robots.txt` sitemaps (including `.xml.gz`), follows sitemap indexes (up to 25 sitemaps), falls back to `/sitemap.xml` and `/sitemap_index.xml`, then crawls page links breadth-first from the start page. URLs are canonicalized (fragments and tracking parameters removed), deduplicated, and limited to the site's host and its `www` twin.

The crawl is built to find the right pages, not every page:

- **Depth.** `--depth 1` reads only the start page; the default `2` also reads the pages it links to. At most 200 pages are fetched.
- **Sampling.** Every discovered URL is listed, but only the first 20 pages of one URL shape (host, first path segment, segment count, query names) are fetched for further links, so 5,000 tag pages cost 20 fetches.
- **Duplicates.** A page whose visible text is a near-duplicate (simhash within 3 bits, at least 200 characters) of one already read adds its links to the list but is not expanded.
- **Robots.** Pages that the `User-agent: *` rules of `robots.txt` disallow are listed when linked but never fetched; `robotsSkipped` counts them.
- **Scripts.** Same-site `<script src>` files (up to 20) and inline scripts are scanned for endpoint paths such as `/api/users`, reported as `endpoints`. Static assets are ignored.
- **Templates.** `--json` groups the URLs into path templates such as `/tag/*` with a count and sample URLs, so one sample per page type can be scraped.
- **Browser.** `--render auto` loads a page in the browser service only when the HTTP result needs JavaScript (up to 25 pages); `always` renders every crawled page up to that cap. `--explore` also scrolls and clicks safe controls on the start page (up to 5 pages) and lists the URLs it reaches; it needs `--render auto` or `always`. Without a browser service these options fall back to HTTP with a warning.

## JSON report

`--json` returns the URLs with the source that found each one, the page templates, and the endpoint paths found in scripts:

```json
{
  "url": "https://example.com/",
  "outcome": "ok",
  "urls": [
    { "url": "https://example.com/pricing", "source": "sitemap" },
    { "url": "https://example.com/blog/hello", "source": "link" }
  ],
  "summary": { "urlsFound": 214, "sitemapsRead": 2, "pagesCrawled": 61, "pagesRendered": 3, "robotsSkipped": 4, "durationMs": 5210, "limited": false },
  "endpoints": ["https://example.com/api/products"],
  "templates": [{ "pattern": "/blog/*", "count": 120, "samples": ["https://example.com/blog/hello"] }]
}
```

Scrape one sample of each template rather than every URL. The command exits `1` only when no URL was found and nothing could be loaded.

## API

```sh
curl -sS http://localhost:8083/map \
  -H 'content-type: application/json' \
  -d '{"url":"https://example.com","depth":3,"render":"auto"}'
```

`POST /map` takes `{"url", "limit", "maxDurationMs", "subdomains", "include", "exclude", "depth", "render", "explore"}` and returns the same report.

## Benchmark

`scripts/discovery-bench.sh <site>` compares a map against Katana, when installed, and prints URL counts per source. See [what PageLode can take from Katana](katana.md).
