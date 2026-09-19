import { once } from "node:events";
import { mkdir } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { chromium, type BrowserContext, type Page } from "patchright";
import { ZodError } from "zod";
import { renderRequestSchema, type RenderData, type RenderEnvelope, type RenderRequest } from "./contract";

let contextPromise: Promise<BrowserContext> | undefined;
let activeProxyURL: string | undefined;
let writes = Promise.resolve();
type PersistentContextOptions = NonNullable<Parameters<typeof chromium.launchPersistentContext>[1]>;
type ProxySettings = NonNullable<PersistentContextOptions["proxy"]>;

function debug(event: string, values: Record<string, unknown> = {}): void {
  if (process.env.PAGELODE_DEBUG !== "true") return;
  process.stderr.write(`${JSON.stringify({ component: "patchright", event, ...values })}\n`);
}

function booleanEnvironment(name: string, fallback: boolean): boolean {
  const raw = process.env[name];
  if (raw === undefined || raw === "") return fallback;
  if (raw === "true") return true;
  if (raw === "false") return false;
  throw new Error(`${name} must be true or false`);
}

function profileDirectory(): string {
  const configured = process.env.PAGELODE_PATCHRIGHT_PROFILE;
  if (configured !== undefined && configured !== "") return configured;
  if (process.platform === "darwin") return join(homedir(), "Library", "Application Support", "PageLode", "Patchright");
  if (process.platform === "win32") return join(process.env.LOCALAPPDATA ?? homedir(), "PageLode", "Patchright");
  return join(homedir(), ".local", "share", "pagelode", "patchright");
}

async function browserContext(input: RenderRequest): Promise<BrowserContext> {
  if (contextPromise === undefined) {
    activeProxyURL = input.proxy_url;
    const directory = profileDirectory();
    await mkdir(directory, { recursive: true });
    const options = launchOptions(input);
    debug("browser_launch", { channel: options.channel ?? "bundled", headless: options.headless, profile: directory, proxy: input.proxy_url !== undefined });
    contextPromise = chromium.launchPersistentContext(directory, options).catch((error: unknown) => {
      contextPromise = undefined;
      activeProxyURL = undefined;
      throw error;
    });
  }
  if (activeProxyURL !== input.proxy_url) throw new Error("proxy cannot change while the persistent browser is running");
  return contextPromise;
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

function launchOptions(input: RenderRequest): PersistentContextOptions {
  const result: PersistentContextOptions = {
    headless: booleanEnvironment("PAGELODE_PATCHRIGHT_HEADLESS", false),
    locale: "en-GB",
    timezoneId: "Europe/London",
    viewport: null,
  };
  const executablePath = process.env.PAGELODE_PATCHRIGHT_EXECUTABLE_PATH;
  if (executablePath !== undefined && executablePath !== "") result.executablePath = executablePath;
  const channel = process.env.PAGELODE_PATCHRIGHT_CHANNEL ?? (process.platform === "linux" ? "" : "chrome");
  if (channel !== "") result.channel = channel;
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

async function settleChallenge(page: Page): Promise<void> {
  for (let attempt = 0; attempt < 3 && await challenged(page); attempt += 1) {
    const target = await challengeTarget(page);
    debug("challenge_detected", { attempt: attempt + 1, title: await page.title(), target: target !== undefined });
    if (target !== undefined) {
      const box = await target.boundingBox();
      if (box !== null) {
        const x = box.x + box.width * (0.45 + Math.random() * 0.1);
        const y = box.y + box.height * (0.45 + Math.random() * 0.1);
        await page.mouse.move(x, y, { steps: 12 + Math.floor(Math.random() * 9) });
        await page.waitForTimeout(150 + Math.floor(Math.random() * 250));
        await page.mouse.click(x, y);
        debug("challenge_clicked", { attempt: attempt + 1 });
      }
    }
    for (let elapsed = 0; elapsed < 15_000 && await challenged(page); elapsed += 500) await page.waitForTimeout(500);
  }
  debug("challenge_settled", { challenged: await challenged(page), title: await page.title(), url: page.url() });
}

async function render(input: RenderRequest): Promise<RenderData> {
  const context = await browserContext(input);
  const page = await context.newPage();
  try {
    let statusCode = 0;
    page.on("response", (response) => {
      if (response.request().resourceType() === "document" && response.frame() === page.mainFrame()) statusCode = response.status();
    });
    await addCookies(page, input);
    const response = await page.goto(input.url, { timeout: input.timeout_ms, waitUntil: input.wait_until });
    debug("navigation_complete", { status: response?.status() ?? 0, title: await page.title(), url: page.url() });
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
    await page.close();
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
if (contextPromise !== undefined) await (await contextPromise).close();
