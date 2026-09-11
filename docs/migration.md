# OpenExtract → PageLode rewrite plan

PageLode is a parallel Go-first implementation. OpenExtract remains deployable
until the new service demonstrates equal or better extraction quality and block
rate on the same corpus.

## Goals

- Preserve `POST /extract` response compatibility.
- Keep managed providers outside the service.
- Make cheap HTTP the common path without returning JavaScript shells or block pages.
- Separate “needs rendering” from “blocked”; these require different browsers.
- Keep Patchright behind a small correlated NDJSON subprocess interface.
- Reuse browser processes and bound all concurrency, queues, bodies, and output.
- Record enough attempt evidence to tune routing without logging secrets.

## Target architecture

```text
caller
  -> PageLode HTTP API (Go)
     -> host policy + learned route
     -> browser-profiled HTTP
        -> accept -----------------------> extraction (Go)
        -> JavaScript shell -> Rod ------> extraction (Go)
        -> block -----------> Patchright -> extraction (Go)
     -> Markdown, links, attempts

Patchright adapter (TypeScript)
  -> validate one render request
  -> create isolated context
  -> seed UA/cookies/proxy
  -> navigate and settle challenge
  -> return rendered HTML, browser cookies, and effective user agent
```

## Phases

### 1. Compatibility foundation

- Implement `/healthz` and `/extract`.
- Preserve OpenExtract outcomes, content type, provider, links, and attempts.
- Add a compatible Go CLI after the API stabilizes.

### 2. Smart retrieval

- Port the structural anti-bot checks from the current OpenExtract design.
- Add Spider-style weighted JavaScript-render detection.
- Route confirmed blocks directly to Patchright instead of wasting a Rod attempt.
- Carry the HTTP user agent and cookies into browser fallbacks.
- Learn successful browser routes per hostname with an expiring in-memory cache.

### 3. Extraction parity

- Use Readability for article-like pages.
- Fall back to cleaned body HTML converted to Markdown.
- Preserve same-site links and useful JSON-LD facts.
- Add PDF extraction and fixture parity before production cutover.

### 4. Browser hardening

- Reuse Rod and Patchright browser processes.
- Add proxy/fingerprint coherence and persistent named sessions where needed.
- Port optional Turnstile token-provider support behind the browser contract.
- Add browser recycling, request interception, and per-domain wait profiles.

### 5. Evaluation and cutover

- Run both services against the existing top-100 and protected-domain corpus.
- Compare success, false acceptance, Markdown quality, latency, CPU, and RSS.
- Shadow production requests before changing callers.
- Keep rollback as an endpoint/configuration change until the observation window closes.

## Initial acceptance gates

- No JavaScript shell may be reported as `outcome=ok` in the fixture suite.
- Confirmed challenge responses must skip Rod.
- Patchright must receive coherent UA, cookies, locale, timezone, and proxy settings.
- Every attempt must have a bounded timeout and a recorded classification reason.
- The service must reject saturated queues before starting more browser work.

## Known first-slice gaps

- HTML and text are supported first; PDF extraction is a cutover blocker.
- Learned routes are process-local; a shared store is unnecessary until multiple replicas need it.
- External CAPTCHA token providers are not in the first slice.
- Domain-specific interaction recipes are deferred until corpus evidence justifies them.

## Baseline result

On 2026-09-11, a live request for the public OpenAI organization page on Crunchbase escalated directly to Patchright, waited through challenge handling, and was still served a Cloudflare HTTP 403 from the local network. PageLode classified it as blocked and returned `outcome=failed` instead of accepting challenge markup as content. A coherent residential proxy plus solver support remains required before Crunchbase can be an acceptance target.
