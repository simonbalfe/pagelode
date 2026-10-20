#!/bin/sh
set -eu

if [ "$#" -lt 1 ]; then
	printf 'usage: %s <domain-or-url> [pagelode-depth]\n' "$0" >&2
	printf 'environment: PAGELODE_BIN (default bin/pagelode), KATANA_DEPTH (default 5), RENDER (default never)\n' >&2
	exit 2
fi

target="$1"
depth="${2:-2}"
pagelode_bin="${PAGELODE_BIN:-bin/pagelode}"
katana_depth="${KATANA_DEPTH:-5}"
render="${RENDER:-never}"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT INT TERM
export LC_ALL=C

case "$target" in
	*://*) url="$target" ;;
	*) url="https://$target" ;;
esac

canonical() {
	sed 's/[#?].*//; s#/$##' | sort -u
}

"$pagelode_bin" map --json --subdomains --limit 5000 --max-duration 90s --depth "$depth" --render "$render" "$url" > "$work_dir/pagelode.json"
jq -r '.urls[].url' "$work_dir/pagelode.json" | canonical > "$work_dir/pagelode.txt"

printf 'pagelode map (depth %s, render %s)\n' "$depth" "$render"
jq -r '"  urls \(.summary.urlsFound), pages crawled \(.summary.pagesCrawled), rendered \(.summary.pagesRendered // 0), robots skipped \(.summary.robotsSkipped // 0), endpoints \(.endpoints // [] | length), \(.summary.durationMs) ms"' "$work_dir/pagelode.json"
printf '  by source:\n'
jq -r '.urls | group_by(.source)[] | "    \(.[0].source): \(length)"' "$work_dir/pagelode.json"
printf '  top templates:\n'
jq -r '.templates // [] | .[:8][] | "    \(.pattern): \(.count)"' "$work_dir/pagelode.json"

if command -v katana >/dev/null 2>&1; then
	katana -u "$url" -d "$katana_depth" -jc -kf all -silent -nc -fs rdn -ct 90s -ef png,jpg,jpeg,svg,gif,css,woff,woff2,ico,webp,js | canonical > "$work_dir/katana.txt"
	both="$(comm -12 "$work_dir/pagelode.txt" "$work_dir/katana.txt" | wc -l | tr -d ' ')"
	only_katana="$(comm -13 "$work_dir/pagelode.txt" "$work_dir/katana.txt" | wc -l | tr -d ' ')"
	only_pagelode="$(comm -23 "$work_dir/pagelode.txt" "$work_dir/katana.txt" | wc -l | tr -d ' ')"
	printf 'katana (depth %s)\n  urls %s\n' "$katana_depth" "$(wc -l < "$work_dir/katana.txt" | tr -d ' ')"
	printf 'overlap\n  both %s, only katana %s, only pagelode %s\n' "$both" "$only_katana" "$only_pagelode"
	printf '  sample only katana:\n'
	comm -13 "$work_dir/pagelode.txt" "$work_dir/katana.txt" | sed -n '1,10s/^/    /p'
else
	printf 'katana is not installed; skipping the comparison\n'
fi
