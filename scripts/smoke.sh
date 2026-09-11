#!/bin/sh
set -eu

fixture_port="${PAGELODE_SMOKE_FIXTURE_PORT:-38084}"
api_port="${PAGELODE_SMOKE_API_PORT:-38083}"
work_dir="$(mktemp -d)"
api_pid=""
fixture_pid=""

cleanup() {
	for pid in "$api_pid" "$fixture_pid"; do
    if [ -n "$pid" ]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  rm -rf "$work_dir"
}

trap cleanup EXIT INT TERM

printf '%s\n' '<!doctype html><html><head><title>PageLode fixture</title></head><body><main><h1>PageLode fixture</h1><p>This local document verifies that retrieval, classification, extraction, and provider routing all cross the expected service boundaries successfully.</p></main></body></html>' > "$work_dir/index.html"
printf '%s\n' '<!doctype html><html><head><title>PageLode shell</title></head><body><div id="app"></div><script>const app = document.querySelector("#app"); const heading = document.createElement("h1"); heading.textContent = "Rendered by Rod"; app.appendChild(heading); const copy = document.createElement("p"); copy.textContent = "This content only exists after JavaScript executes, proving that PageLode escalated an application shell into its ordinary browser renderer."; app.appendChild(copy);</script></body></html>' > "$work_dir/shell.html"
python3 -m http.server "$fixture_port" --bind 127.0.0.1 --directory "$work_dir" >"$work_dir/fixture.log" 2>&1 &
fixture_pid="$!"

PORT="$api_port" PAGELODE_PATCHRIGHT_WORKER="browser/src/worker.ts" PAGELODE_PROTECTED_DOMAINS="localhost" PAGELODE_ROD_ENABLED=true go run ./cmd/pagelode >"$work_dir/api.log" 2>&1 &
api_pid="$!"

attempt=0
until curl -fsS "http://127.0.0.1:$api_port/healthz" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
	if [ "$attempt" -ge 80 ]; then
		sed -n '1,200p' "$work_dir/api.log"
		exit 1
  fi
  sleep 0.25
done

tls_result="$(curl -fsS "http://127.0.0.1:$api_port/extract" -H 'content-type: application/json' -d "{\"url\":\"http://127.0.0.1:$fixture_port/index.html\"}")"
rod_result="$(curl -fsS "http://127.0.0.1:$api_port/extract" -H 'content-type: application/json' -d "{\"url\":\"http://127.0.0.1:$fixture_port/shell.html\"}")"
patchright_result="$(curl -fsS "http://127.0.0.1:$api_port/extract" -H 'content-type: application/json' -d "{\"url\":\"http://localhost:$fixture_port/index.html\"}")"

printf '%s' "$tls_result" | grep -q '"provider":"tls"'
printf '%s' "$tls_result" | grep -q '"outcome":"ok"'
printf '%s' "$rod_result" | grep -q '"provider":"rod"'
printf '%s' "$rod_result" | grep -q '"outcome":"ok"'
printf '%s' "$patchright_result" | grep -q '"provider":"patchright"'
printf '%s' "$patchright_result" | grep -q '"outcome":"ok"'

printf '%s\n' 'PageLode smoke test passed: tls -> rod -> patchright'
