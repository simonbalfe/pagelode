import { createHash } from "node:crypto";
import { chromium, type BrowserContext, type Page } from "patchright";
import { captureTraffic } from "./capture.ts";
import type { Config } from "./config.ts";
import { ServiceError, type RenderRequest, type RenderResult } from "./contract.ts";
import type { Identities } from "./identities.ts";
import { launchOptions, type Log } from "./launch.ts";
import type { Limiter } from "./limiter.ts";
import { personaLaunch, resolvePersona, withProxySession, writePreferences } from "./persona.ts";
import { profileDirectory, restoreSession, saveSession, type ProfileLocks } from "./profiles.ts";

const renderGraceMs = 15_000;

export class Renderer {
  readonly #config: Config;
  readonly #limiter: Limiter;
  readonly #locks: ProfileLocks;
  readonly #identities: Identities;
  readonly #log: Log;

  constructor(config: Config, limiter: Limiter, locks: ProfileLocks, identities: Identities, log: Log) {
    this.#config = config;
    this.#limiter = limiter;
    this.#locks = locks;
    this.#identities = identities;
    this.#log = log;
  }

  async render(input: RenderRequest, signal: AbortSignal): Promise<RenderResult> {
    try {
      if (input.profile !== undefined) return await this.#renderProfile(input, input.profile, signal);
      return await this.#limiter.run(signal, () => this.#renderAnonymous(input, signal));
    } catch (error: unknown) {
      if (error instanceof ServiceError) throw error;
      if (signal.aborted) throw signal.reason;
      throw new ServiceError(502, "render_failed", error instanceof Error ? error.message : "browser render failed");
    }
  }

  async #renderAnonymous(input: RenderRequest, signal: AbortSignal): Promise<RenderResult> {
    const key = input.identity ?? (input.proxy_url === undefined ? "default" : `proxy-${createHash("sha256").update(input.proxy_url).digest("hex").slice(0, 12)}`);
    const lease = this.#identities.acquire(key, input.proxy_url);
    const deadline = Promise.withResolvers<never>();
    const timer = setTimeout(() => {
      lease.discard();
      deadline.reject(new ServiceError(504, "render_failed", `render exceeded ${input.timeout_ms + renderGraceMs} ms; browser restarted`));
    }, input.timeout_ms + renderGraceMs);
    try {
      return await Promise.race([lease.context.then((context) => renderPage(context, input, signal, this.#log)), deadline.promise]);
    } finally {
      clearTimeout(timer);
      lease.release();
    }
  }

  async #renderProfile(input: RenderRequest, name: string, signal: AbortSignal): Promise<RenderResult> {
    const directory = await profileDirectory(this.#config.profilesDirectory, name, false);
    return this.#locks.withRender(name, () => this.#limiter.run(signal, async () => {
      const proxyURL = withProxySession(input.proxy_url, `profile:${name}`);
      const persona = await resolvePersona(proxyURL, this.#log);
      await writePreferences(directory, persona, proxyURL !== undefined);
      const { env, args } = personaLaunch(persona);
      const context = await chromium.launchPersistentContext(directory, launchOptions(this.#config, { headless: this.#config.headless, proxyURL, args, env }));
      try {
        await restoreSession(context, directory);
        const result = await renderPage(context, input, signal, this.#log);
        await saveSession(context, directory);
        return result;
      } finally {
        await context.close();
      }
    }));
  }
}

async function renderPage(context: BrowserContext, input: RenderRequest, signal: AbortSignal, log: Log): Promise<RenderResult> {
  const page = await context.newPage();
  const closePage = (): void => {
    void page.close().catch(() => log("page_close_failed"));
  };
  signal.addEventListener("abort", closePage, { once: true });
  const captureDeadline = input.capture === undefined ? undefined : setTimeout(closePage, input.timeout_ms);
  try {
    const capture = input.capture === undefined ? undefined : captureTraffic(page);
    let statusCode = 0;
    page.on("response", (response) => {
      if (response.request().resourceType() === "document" && response.frame() === page.mainFrame()) statusCode = response.status();
    });
    if (input.cookies.length > 0) await addCookies(page, input);
    const response = await page.goto(input.url, { timeout: input.timeout_ms, waitUntil: input.wait_until });
    log("navigation_complete", { status: response?.status() ?? 0, title: await page.title(), url: page.url() });
    if (input.settle_challenge) await settleChallenge(page, log);
    await page.waitForTimeout(input.capture?.wait_ms ?? 500);
    const cookies = await context.cookies();
    return {
      status_code: statusCode || response?.status() || 0,
      final_url: page.url(),
      title: await page.title(),
      html: await page.content(),
      user_agent: await page.evaluate(() => navigator.userAgent),
      cookies: cookies.map((cookie) => ({
        name: cookie.name,
        value: cookie.value,
        domain: cookie.domain,
        path: cookie.path,
        expires: cookie.expires,
        http_only: cookie.httpOnly,
        secure: cookie.secure,
        same_site: cookie.sameSite,
      })),
      ...(capture === undefined ? {} : { traffic: await capture.finish() }),
    };
  } finally {
    clearTimeout(captureDeadline);
    signal.removeEventListener("abort", closePage);
    await page.close();
  }
}

async function addCookies(page: Page, input: RenderRequest): Promise<void> {
  const origin = new URL(input.url);
  await page.context().addCookies(input.cookies.map((cookie) => ({
    name: cookie.name,
    value: cookie.value,
    domain: cookie.domain ?? origin.hostname,
    path: cookie.path ?? "/",
    httpOnly: cookie.http_only ?? false,
    secure: cookie.secure ?? origin.protocol === "https:",
    ...(cookie.expires === undefined ? {} : { expires: cookie.expires }),
    ...(cookie.same_site === undefined ? {} : { sameSite: cookie.same_site }),
  })));
}

async function challenged(page: Page): Promise<boolean> {
  const title = (await page.title()).toLowerCase();
  if (["just a moment", "one moment", "attention required", "security verification", "checking your browser"].some((value) => title.includes(value))) return true;
  const body = (await page.locator("body").innerText({ timeout: 2_000 }).catch(() => "")).toLowerCase();
  if (["verify you are human", "verify your session", "performing security verification", "cloudflare ray id", "enable javascript and cookies"].some((value) => body.includes(value))) return true;
  return (await page.locator("#challenge-form, form[action*='/cdn-cgi/challenge-platform'], iframe[src*='challenges.cloudflare.com'], .cf-turnstile").count()) > 0;
}

async function challengeTarget(page: Page) {
  const frames = [page.mainFrame(), ...page.frames().filter((frame) => frame !== page.mainFrame())];
  for (const frame of frames) {
    for (const selector of ["input[type=checkbox]", "[role=checkbox]", ".cf-turnstile", "[data-sitekey]"]) {
      const candidate = frame.locator(selector).first();
      if (await candidate.isVisible({ timeout: 500 }).catch(() => false)) return candidate;
    }
  }
  return undefined;
}

async function settleChallenge(page: Page, log: Log): Promise<void> {
  for (let attempt = 0; attempt < 3 && await challenged(page); attempt += 1) {
    const target = await challengeTarget(page);
    log("challenge_detected", { attempt: attempt + 1, title: await page.title(), target: target !== undefined });
    if (target !== undefined) {
      const box = await target.boundingBox();
      if (box !== null) {
        const x = box.x + box.width * (0.45 + Math.random() * 0.1);
        const y = box.y + box.height * (0.45 + Math.random() * 0.1);
        await page.mouse.move(x, y, { steps: 12 + Math.floor(Math.random() * 9) });
        await page.waitForTimeout(150 + Math.floor(Math.random() * 250));
        await page.mouse.click(x, y);
        log("challenge_clicked", { attempt: attempt + 1 });
      }
    }
    for (let elapsed = 0; elapsed < 15_000 && await challenged(page); elapsed += 500) await page.waitForTimeout(500);
  }
  log("challenge_settled", { challenged: await challenged(page), title: await page.title(), url: page.url() });
}
