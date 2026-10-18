#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
crawl="${CRAWL:-CC-MAIN-2026-39}"
files="${FILES:-100}"
rows_per_file="${ROWS:-3000}"
parallel="${PARALLEL:-8}"
output="${OUT:-$root/data/commoncrawl.duckdb}"
base="https://data.commoncrawl.org"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$(dirname "$output")"

curl -sSf "$base/crawl-data/$crawl/cc-index-table.paths.gz" | gunzip | grep 'subset=warc' >"$work/paths.txt"
sort -R "$work/paths.txt" | head -n "$files" >"$work/selected.txt"

sample_file() {
  local path="$1" rows="$2" work="$3" base="$4"
  local name
  name="$(basename "$path" .zstd.parquet)"
  duckdb -c "
    INSTALL httpfs;
    LOAD httpfs;
    COPY (
      SELECT
        url,
        url_host_name AS host,
        url_host_registered_domain AS domain,
        url_host_tld AS tld,
        url_protocol AS protocol,
        fetch_status,
        content_mime_detected AS mime,
        content_languages AS languages,
        content_truncated IS NOT NULL AS truncated,
        fetch_time,
        warc_filename,
        warc_record_offset,
        warc_record_length,
        '$name' AS source_part
      FROM read_parquet('$base/$path')
      USING SAMPLE $rows ROWS
    ) TO '$work/$name.parquet' (FORMAT parquet);
  " >/dev/null
}
export -f sample_file

xargs -P "$parallel" -I{} bash -c 'sample_file "$@"' _ {} "$rows_per_file" "$work" "$base" <"$work/selected.txt"

rm -f "$output"
duckdb "$output" -c "
  CREATE TABLE urls AS
  SELECT DISTINCT ON (url) *, '$crawl' AS crawl
  FROM read_parquet('$work/*.parquet')
  ORDER BY url;
  CREATE INDEX urls_domain_idx ON urls (domain);
  SELECT count(*) AS urls, count(DISTINCT domain) AS domains, count(DISTINCT tld) AS tlds FROM urls;
"
