import { once } from "node:events";
import { createInterface } from "node:readline";
import { chromium, type Browser, type BrowserContextOptions, type Page } from "patchright";
import { ZodError } from "zod";
import { renderRequestSchema, type RenderData, type RenderEnvelope, type RenderRequest } from "./contract";

let browserPromise: Promise<Browser> | undefined;
let writes = Promise.resolve();
type ProxySettings = NonNullable<BrowserContextOptions["proxy"]>;

function booleanEnvironment(name: string, fallback: boolean): boolean {
  const raw = process.env[name];
  if (raw === undefined || raw === "") return fallback;
  if (raw === "true") return true;
  if (raw === "false") return false;
  throw new Error(`${name} must be true or false`);
}

async function browser(): Promise<Browser> {
  if (browserPromise === undefined) {
    const headless = booleanEnvironment("PAGELODE_PATCHRIGHT_HEADLESS", true);
    browserPromise = chromium.launch({ headless }).catch((error: unknown) => {
      browserPromise = undefined;
      throw error;
    });
  }
  return browserPromise;
}

function proxySettings(raw: string | undefined): ProxySettings | undefined {
  if (raw === undefined || raw === "") return undefined;
  const parsed = new URL(raw);
  if (!["http:", "https:", "socks4:", "socks5:"].includes(parsed.protocol)) throw new Error("unsupported proxy protocol");
  const settings: ProxySettings = { server: `${parsed.protocol}//${parsed.host}` };
  if (parsed.username !== "") settings.username = decodeURIComponent(parsed.username);
  if (parsed.password !== "") settings.password = decodeURIComponent(parsed.password);
  return settings;
}

function contextOptions(input: RenderRequest): BrowserContextOptions {
  const result: BrowserContextOptions = {
    locale: "en-GB",
    timezoneId: "Europe/London",
  };
  if (input.user_agent !== undefined) result.userAgent = input.user_agent;
  const proxy = proxySettings(input.proxy_url);
  if (proxy !== undefined) result.proxy = proxy;
  return result;
}

async function addCookies(page: Page, input: RenderRequest): Promise<void> {
  if (input.cookies.length === 0) return;
  const origin = new URL(input.url);
  await page.context().addCookies(input.cookies.map((cookie) => {
    const sameSite = cookie.same_site;
    return {
      name: cookie.name,
      value: cookie.value,
      domain: cookie.domain ?? origin.hostname,
      path: cookie.path ?? "/",
      httpOnly: cookie.http_only ?? false,
      secure: cookie.secure ?? origin.protocol === "https:",
      ...(cookie.expires === undefined ? {} : { expires: cookie.expires }),
      ...(sameSite === undefined ? {} : { sameSite }),
    };
  }));
}

async function challenged(page: Page): Promise<boolean> {
  const title = (await page.title()).toLowerCase();
  if (["just a moment", "attention required", "security verification", "checking your browser"].some((value) => title.includes(value))) return true;
  const body = (await page.locator("body").innerText({ timeout: 2_000 }).catch(() => "")).toLowerCase();
  return ["verify you are human", "performing security verification", "cloudflare ray id", "enable javascript and cookies"].some((value) => body.includes(value));
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

async function settleChallenge(page: Page): Promise<void> {
  for (let attempt = 0; attempt < 3 && await challenged(page); attempt += 1) {
    const target = await challengeTarget(page);
    if (target !== undefined) {
      const box = await target.boundingBox();
      if (box !== null) {
        const x = box.x + box.width * (0.45 + Math.random() * 0.1);
        const y = box.y + box.height * (0.45 + Math.random() * 0.1);
        await page.mouse.move(x, y, { steps: 12 + Math.floor(Math.random() * 9) });
        await page.waitForTimeout(150 + Math.floor(Math.random() * 250));
        await page.mouse.click(x, y);
      }
    }
    for (let elapsed = 0; elapsed < 15_000 && await challenged(page); elapsed += 500) await page.waitForTimeout(500);
  }
}

async function render(input: RenderRequest): Promise<RenderData> {
  const activeBrowser = await browser();
  const context = await activeBrowser.newContext(contextOptions(input));
  try {
    const page = await context.newPage();
    let statusCode = 0;
    page.on("response", (response) => {
      if (response.request().resourceType() === "document" && response.frame() === page.mainFrame()) statusCode = response.status();
    });
    await addCookies(page, input);
    const response = await page.goto(input.url, { timeout: input.timeout_ms, waitUntil: input.wait_until });
    if (input.settle_challenge) await settleChallenge(page);
    await page.waitForTimeout(500);
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
    };
  } finally {
    await context.close();
  }
}

async function write(envelope: RenderEnvelope): Promise<void> {
  writes = writes.then(async () => {
    if (!process.stdout.write(`${JSON.stringify(envelope)}\n`)) await once(process.stdout, "drain");
  });
  await writes;
}

async function handle(line: string): Promise<void> {
  let id = "";
  try {
    const raw: unknown = JSON.parse(line);
    if (typeof raw === "object" && raw !== null && "id" in raw && typeof raw.id === "string") id = raw.id;
    const input = renderRequestSchema.parse(raw);
    await write({ id: input.id, ok: true, data: await render(input) });
  } catch (error: unknown) {
    if (error instanceof ZodError || error instanceof SyntaxError) {
      await write({ id, ok: false, error: { code: "invalid_request", message: "request is invalid" } });
      return;
    }
    const message = error instanceof Error ? error.message : "browser render failed";
    await write({ id, ok: false, error: { code: "render_failed", message } });
  }
}

const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
const active = new Set<Promise<void>>();

for await (const line of input) {
  if (line.trim() === "") continue;
  const job = handle(line).finally(() => active.delete(job));
  active.add(job);
}

await Promise.allSettled(active);
await writes;
if (browserPromise !== undefined) await (await browserPromise).close();
