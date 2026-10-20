# Browser stack decision

Which process owns browsers, and why PageLode does not build a Go-native stealth browser. [Browser service](browser-service.md) describes the service as it runs today; this page records the decision and the work it implied.

**Status: executed on 19 October 2026.** Chromedp was removed without running the [removal gate](#chromedp-removal-gate), at the user's request. Every browser now runs in the browser service.

## Verdict

| Decision | Detail |
|---|---|
| **The Go core becomes browserless** (done) | Go keeps HTTP (tls-client), classification, routing, route memory, clearance replay, identity keys and proxy sessions, and every CapSolver task. It drives no browser. |
| **One Bun browser service owns every browser** (done) | Rendering, capture, identities, profiles, and agent CDP sessions all live in `browser-service/`. |
| **Chromedp is removed** (done, gate skipped) | `internal/chromefetch`, `chromedp` and `cdproto`, `PAGELODE_CHROMEDP_ENABLED`, `PAGELODE_BROWSER_CONCURRENCY` and the Go browser limiter, and the Debian `chromium` package are gone. JavaScript shells go to the service. |
| **No Go-native stealth tier for now** | Build one only if running PageLode without the service becomes a requirement. Then use direct CDP, not chromedp or rod ([requirements](#go-native-stealth-tier-if-ever-needed)). |
| **Camoufox only on evidence** | Add it as a second engine inside the service only when a target blocks headed Chrome on a good proxy ([Camoufox](#camoufox)). |

## Why not a Go-native browser

The question was whether Go could drive its own stealthy Chrome (chromedp, rod, or raw CDP) so the service is not needed.

| Reason | Evidence |
|---|---|
| The service is always deployed | `compose.yaml` runs `browser` next to `pagelode`; `PAGELODE_BROWSER_URL` defaults to `http://127.0.0.1:8090` for the CLI too. Blocked pages, profiles, and `--stealth` need it anyway. |
| Stealth on a server is mostly environment, not code | The service passes bot checks with headed Google Chrome on Xvfb, fluxbox, PulseAudio, Mesa WebGL, Linux fonts, and a persona applied natively. A Go browser on a server needs the same container. |
| The service is more than Patchright's patches | Identities, rotation, persona, profile locks, the CDP relay, render deadlines, challenge settling, and DataDome waits would all be rebuilt in Go. |
| Bun costs almost nothing per render | A render used to wait a fixed 500 ms after load (about 85% of a single render); it now stops when the page goes quiet. The service call is a local HTTP hop. |
| Agents need Node regardless | Agents attach to service sessions with Patchright on Node; Bun's Playwright client cannot `connectOverCDP`. |

A Go-native stealth browser only pays off on a desktop, where the real Chrome, display, GPU, and fonts come for free.

## Stealth measurements

Measured on 19 October 2026 on an Apple M4 Pro, home IP, Google Chrome 155.0.8059.84, headed unless stated. The main signal set is [bot-detector.rebrowser.net](https://bot-detector.rebrowser.net/) (15 checks), with its leak tests triggered by calling `document.getElementById` and `document.getElementsByClassName` from the driver.

| Driver | Rebrowser checks that failed | Other sites |
|---|---|---|
| Stock Playwright (`playwright-go` v0.6201.1, `playwright-core` 1.62.1) | Headless run: `navigatorWebdriver`, `runtimeEnableLeak`, `sourceUrlLeak` (`UtilityScript.` in the stack), `mainWorldExecution`, `headless` | Headed: deviceandbrowserinfo bot; BrowserScan Robot; CreepJS headless 33% |
| `patchright-core` 1.62.1 under `playwright-go` | `sourceUrlLeak`, `mainWorldExecution` | deviceandbrowserinfo: human; BrowserScan: Normal; CreepJS headless 0% |
| Same, with `playwright-go` patched to send `isolatedContext: true` | none | — |
| rod v0.116.2 with `enable-automation` removed, `AutomationControlled` disabled, `NoDefaultDevice()` | `mainWorldExecution` (`page.Eval` runs in the main world) | deviceandbrowserinfo: human; BrowserScan: Normal; CreepJS headless 0% |
| Same, evaluating in an isolated world (`PageCreateIsolatedWorld` + `RuntimeEvaluate{ContextID}`) | none | — |
| rod defaults, headless | `navigatorWebdriver`, `mainWorldExecution`, `platformMismatch` (default device emulation); headless, user agent, and worker consistency marked suspicious | — |
| chromedp v0.21.0 with `enable-automation` removed and `AutomationControlled` disabled | `runtimeEnableLeak` | — |
| Direct CDP over `--remote-debugging-pipe`, Go standard library only, no `Runtime.enable`, isolated-world evaluation, `Input` clicks | none | deviceandbrowserinfo: human |

Findings:

- **Headless is caught.** In the headless run, every driver failed the rebrowser `headless` check except rod's defaults, which were marked suspicious because its device emulation replaces the `HeadlessChrome` user agent.
- **Patchright's isolation lives in its client.** `patchright-core` evaluates in the main world unless the client sends `isolatedContext: true`; Patchright's Node and Python clients default it to true, `playwright-go` never sends it.
- **chromedp cannot drop `Runtime.enable` without a fork.** `chromedp.go` enables it on every attached target, and `target.go` tracks execution contexts from the events it produces.
- **rod already avoids `Runtime.enable`**, but `page.Eval` runs in the main world. Its element helpers also call page JavaScript; that path was not measured.
- **`--remote-debugging-pipe` sets `navigator.webdriver`** even without `--enable-automation`; `--disable-blink-features=AutomationControlled` clears it.
- **Patchright cannot drive Camoufox usefully.** It patches only Chromium, and `camoufox-js` requires `playwright-core` below 1.63 while the service runs Patchright 1.63.0.

## The former Chromedp tier

Recorded before removal. `internal/chromefetch` (422 lines) used chromedp mostly as a CDP transport for `network` and `fetch` calls, plus `Navigate`, `Sleep`, and `OuterHTML`. It was the most detectable loader:

| Signal | Cause |
|---|---|
| `navigator.webdriver` true | `chromedp.DefaultExecAllocatorOptions` includes `--enable-automation` |
| Headless | The same defaults |
| `Runtime.enable` leak | chromedp enables it on attach |
| Platform mismatch | `emulation.SetUserAgentOverride(...).WithPlatform("MacIntel")` on every OS, without matching client hints |
| Chromium branding (not measured) | The Go image installed Debian `chromium`, not Google Chrome; rebrowser's `useragent` check already treats Chrome for Testing as a red flag |

This was acceptable only because routing sent it unprotected JavaScript shells and escalated blocked results to Patchright.

## Improvements

| Order | Change | Status |
|---|---|---|
| 1 | Run the [Chromedp removal gate](#chromedp-removal-gate) | Skipped at the user's request; Chromedp was removed on 19 October 2026 without it |
| 2 | Interim hardening of Chromedp flags and the `MacIntel` override | Dropped: Chromedp is gone |
| 3 | Remove Chromedp: delete `internal/chromefetch`, the Chromedp routes and route memory entries, `PAGELODE_CHROMEDP_ENABLED`, and `chromium` from the Go `Dockerfile`; send JavaScript shells to the service | Done 19 October 2026; architecture and routing docs updated. `PAGELODE_BROWSER_CONCURRENCY` and the Go browser limiter were removed too |
| 4 | Replace the fixed post-load wait and stop scanning every page for challenges | Done: the service waits until the page goes quiet, capped by `settle_ms` (default 2000, from `PAGELODE_BROWSER_SETTLE_MS`), and runs challenge checks only when a page shows a hint. `wait_for` is available on `/v1/render`. 32 static renders at 8 at once went from 12.2 to 25.0 pages/s; see [quiet wait results](browser-service.md#quiet-wait-results) |
| 5 | Keep the rebrowser suite as a stealth regression check for the service after Chrome or Patchright upgrades | Done: `scripts/stealth-check.sh` renders bot-detector.rebrowser.net through the service (`settle_ms` 4000) and exits non-zero on any red check |

`scripts/stealth-check.sh` on 19 October 2026, local headed service: every applicable check green. The trigger-only checks (`dummyFn`, `exposeFunctionLeak`, `mainWorldExecution`, `sourceUrlLeak`) are not triggered by a plain render. Not yet run in the container.

### Chromedp removal gate

Not run. Kept as the measurement that was planned: the same real lead list (about 200 domains) through `pagelode emails --input` twice with the service running, once with Chromedp enabled and once disabled.

| Measure | Remove Chromedp if disabling it |
|---|---|
| Sites with addresses found, total addresses | Loses no yield |
| Total duration and p95 per site | Is not materially slower |
| Chromedp attempts that succeeded versus escalated to Patchright | Shows Chromedp mostly escalates anyway |

## Go-native stealth tier, if ever needed

Build this only if PageLode must run browser rendering without the service, for example the CLI on a laptop. Use direct CDP on the `cdproto` types (no longer a PageLode dependency since Chromedp was removed); a 318-line standard-library prototype passed every check above. It needs four layers; only the first replaces Patchright.

| Layer | Required |
|---|---|
| 1. Automation leaks | Pipe transport; no `Runtime.enable` or `Console.enable`; all JavaScript in isolated worlds; Patchright's flag set (add `--disable-blink-features=AutomationControlled`; omit `--enable-automation`, `--disable-popup-blocking`, `--disable-component-update`, `--disable-default-apps`, `--disable-extensions`); no `Emulation` overrides; no `Runtime.addBinding`; inject any early script into HTML responses through `Fetch` rather than `Page.addScriptToEvaluateOnNewDocument` |
| 2. Real environment | Google Chrome stable, headed. On a server, the service's container: Xvfb 1920×1080×24, fluxbox, PulseAudio, Mesa ANGLE flags, Linux font set, 1 GB shared memory |
| 3. Identity matching the proxy | A port of `persona.ts` and `launch.ts`: exit-IP lookup, `TZ`, `LANG`, `LANGUAGE`, `--lang`, `intl.accept_languages`, WebRTC `disable_non_proxied_udp`, a sticky proxy session per identity, one profile directory per identity, proxy auth through `Fetch.authRequired` |
| 4. Behaviour | Trusted `Input` events for challenge checkboxes, the DataDome device-check wait, human pacing |

Do not add JavaScript "stealth plugins": `navigator` overrides, fake plugins or `chrome.runtime`, user agent or platform spoofing, canvas or audio noise, or WebGL vendor spoofing. Rebrowser's `nativeTampering`, `workerConsistency`, and `platformMismatch` checks detect them. Changing those values convincingly needs a patched browser engine.

Accept it only when it matches the service's results: all 15 rebrowser checks, deviceandbrowserinfo "human", CreepJS headless 0%, BrowserScan "Normal", and the protected targets in [browser service measurements](browser-service.md#stealth-and-rendering).

## Camoufox

Camoufox is Firefox with fingerprint values spoofed in C++, configured per launch through `CAMOU_CONFIG_<n>` environment variables. It would give each identity its own fingerprint, which the service's shared container fingerprint does not.

- Add it inside the service as a second engine: `camoufox-js` with its own `playwright-core` 1.62.x beside `patchright`. They are separate npm packages.
- `/v1/render` gains an `engine` field; each identity is bound to one engine; Go chooses the engine.
- Costs: a 313 MB browser in the image and a second launch path.
- Trigger: a target that still blocks headed Chrome through a good mobile proxy after the [known gaps](browser-service.md#known-gaps) are fixed. No measured target needs it today; Glassdoor failed on challenge detection, not fingerprint, and that detection was fixed on 19 October 2026.

## Stealth test sites

| Site | Use |
|---|---|
| [bot-detector.rebrowser.net](https://bot-detector.rebrowser.net/) | Automation leaks; read `#detections-json`; trigger its leak tests from the driver |
| [deviceandbrowserinfo.com/are_you_a_bot](https://deviceandbrowserinfo.com/are_you_a_bot) | Fingerprint verdict |
| [CreepJS](https://abrahamjuliot.github.io/creepjs/) | Headless, like-headless, and stealth percentages |
| [BrowserScan](https://www.browserscan.net/bot-detection) | WebDriver, CDP, and Robot verdict |

The BotD demo at `fingerprintjs.github.io/BotD/main/` now returns 404, and `nowsecure.nl` only shows a test Turnstile widget.
