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
printf '%s\n' '<!doctype html><html><head><title>PageLode shell</title></head><body><div id="app"></div><script>const app = document.querySelector("#app"); const heading = document.createElement("h1"); heading.textContent = "Rendered by Chromedp"; app.appendChild(heading); const copy = document.createElement("p"); copy.textContent = "This content only exists after JavaScript executes, proving that PageLode escalated an application shell into its ordinary browser renderer."; app.appendChild(copy);</script></body></html>' > "$work_dir/shell.html"
printf '%s\n' '{"items":[{"id":1,"price":125000}],"nextCursor":"smoke-secret"}' > "$work_dir/data.json"
printf '%s\n' '<html><head><title>Discovery fixture</title></head><body><h1>Listings</h1><script>fetch("/data.json?token=smoke-secret", {headers:{Authorization:"Bearer smoke-secret"}});</script></body></html>' > "$work_dir/discover.html"
python3 -m http.server "$fixture_port" --bind 127.0.0.1 --directory "$work_dir" >"$work_dir/fixture.log" 2>&1 &
fixture_pid="$!"

go build -o "$work_dir/pagelode" ./cmd/pagelode
PORT="$api_port" PAGELODE_PATCHRIGHT_HEADLESS=true PAGELODE_PATCHRIGHT_PROFILE="$work_dir/profile" PAGELODE_PATCHRIGHT_WORKER="browser/src/worker.ts" PAGELODE_PROTECTED_DOMAINS="localhost" PAGELODE_CHROMEDP_ENABLED=true "$work_dir/pagelode" serve >"$work_dir/api.log" 2>&1 &
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
chromedp_result="$(curl -fsS "http://127.0.0.1:$api_port/extract" -H 'content-type: application/json' -d "{\"url\":\"http://127.0.0.1:$fixture_port/shell.html\"}")"
patchright_result="$(curl -fsS "http://127.0.0.1:$api_port/extract" -H 'content-type: application/json' -d "{\"url\":\"http://localhost:$fixture_port/index.html\"}")"

printf '%s' "$tls_result" | grep -q '"provider":"tls"'
printf '%s' "$tls_result" | grep -q '"outcome":"ok"'
printf '%s' "$chromedp_result" | grep -q '"provider":"chromedp"'
printf '%s' "$chromedp_result" | grep -q '"outcome":"ok"'
printf '%s' "$patchright_result" | grep -q '"provider":"patchright"'
printf '%s' "$patchright_result" | grep -q '"outcome":"ok"'

for fixture_host in 127.0.0.1 localhost; do
  discovery_result="$(curl -fsS "http://127.0.0.1:$api_port/discover" -H 'content-type: application/json' -d "{\"url\":\"http://$fixture_host:$fixture_port/discover.html\",\"waitMs\":200}")"
  printf '%s' "$discovery_result" | grep -q '"path":"/data.json"'
  printf '%s' "$discovery_result" | grep -q '"kind":"bearer"'
  printf '%s' "$discovery_result" | grep -q '"path":"$.items\[\].price"'
  if printf '%s' "$discovery_result" | grep -q 'smoke-secret'; then
    exit 1
  fi
done
printf '%s\n' '<html><body><main><h1>Contact directory</h1></main><footer><a href="mailto:footer@example.org">Contact</a></footer></body></html>' > "$work_dir/emails.html"
printf '%s\n' '{"people":[{"email":"dynamic@example.org"}],"token":"smoke-secret"}' > "$work_dir/emails.json"
printf '%s\n' '<html><body><h1>Team</h1><script>fetch("/emails.json?token=smoke-secret").then(r=>r.json()).then(data=>{document.body.append(data.people[0].email)})</script></body></html>' > "$work_dir/email-shell.html"
for fixture_host in 127.0.0.1 localhost; do
  email_result="$(curl -fsS "http://127.0.0.1:$api_port/emails" -H 'content-type: application/json' -d "{\"url\":\"http://$fixture_host:$fixture_port/email-shell.html\",\"maxPages\":1}")"
  printf '%s' "$email_result" | grep -q 'dynamic@example.org'
  printf '%s' "$email_result" | grep -q 'network_json'
  if printf '%s' "$email_result" | grep -q 'smoke-secret'; then
    exit 1
  fi
done
"$work_dir/pagelode" emails --max-pages 1 --render never "http://127.0.0.1:$fixture_port/emails.html" > "$work_dir/emails-cli.txt"
grep -qx 'footer@example.org' "$work_dir/emails-cli.txt"
"$work_dir/pagelode" discover --wait-ms 100 "http://127.0.0.1:$fixture_port/discover.html" > "$work_dir/discovery-cli.json"
grep -q '"path": "/data.json"' "$work_dir/discovery-cli.json"
printf '%s\n' 'PageLode smoke test passed: extraction, discovery, and email finding with Chromedp and Patchright'
