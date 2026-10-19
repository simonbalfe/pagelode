#!/bin/sh
set -eu

service_url="${PAGELODE_BROWSER_URL:-http://127.0.0.1:8090}"
token="${PAGELODE_BROWSER_TOKEN:-}"
target="https://bot-detector.rebrowser.net/"

response="$(curl -fsS "$service_url/v1/render" \
  -H 'content-type: application/json' \
  -H "authorization: Bearer $token" \
  -d "{\"url\":\"$target\",\"settle_ms\":4000}")"

checks="$(printf '%s' "$response" | grep -oE '(🔴|🟡|🟢|⚪️) [A-Za-z]+' | sort -u)"
if [ -z "$checks" ]; then
  echo "stealth check: no results found in the rendered $target page" >&2
  exit 1
fi
printf '%s\n' "$checks"
if printf '%s' "$checks" | grep -q '🔴'; then
  echo "stealth check failed: the browser service leaks automation signals" >&2
  exit 1
fi
echo "stealth check passed"
