import type { BrowserContext, Page, Request } from "patchright";
import { clearDataDome } from "../captcha/datadome.ts";
import { clearGoogleSorry } from "../captcha/google.ts";
import type { CapSolverSettings } from "../config.ts";
import type { Log } from "../shared/log.ts";
import { captureTraffic } from "./capture.ts";
import { settleChallenge } from "./challenge.ts";
import { portableCookies, replayHeaders } from "./replay.ts";
import type { RenderRequest, RenderResult } from "./schema.ts";

const settleDelayMs = 500;

export type RenderEnvironment = {
  readonly signal: AbortSignal;
  readonly log: Log;
  readonly proxyURL: string | undefined;
  readonly capsolver: CapSolverSettings | undefined;
};

export async function renderPage(context: BrowserContext, input: RenderRequest, environment: RenderEnvironment): Promise<RenderResult> {
  const { signal, log } = environment;
  const page = await context.newPage();
  const closePage = (): void => {
    void page.close().catch(() => log("page_close_failed"));
  };
  signal.addEventListener("abort", closePage, { once: true });
  const captureDeadline = input.capture === undefined ? undefined : setTimeout(closePage, input.timeout_ms);
  try {
    const capture = input.capture === undefined ? undefined : captureTraffic(page);
    let documentStatus = 0;
    let documentRequest: Request | undefined;
    page.on("response", (response) => {
      if (response.request().resourceType() !== "document" || response.frame() !== page.mainFrame()) return;
      documentStatus = response.status();
      documentRequest = response.request();
    });
    if (input.cookies.length > 0) await addCookies(page, input);
    const response = await page.goto(input.url, { timeout: input.timeout_ms, waitUntil: input.wait_until });
    log("navigation_complete", { status: response?.status() ?? 0, title: await page.title(), url: page.url() });
    if (input.settle_challenge) await settleChallenge(page, log);
    if (input.solve_captcha) {
      await clearDataDome(page, environment);
      await clearGoogleSorry(page, environment);
    }
    await page.waitForTimeout(input.capture?.wait_ms ?? settleDelayMs);
    return {
      status_code: documentStatus || response?.status() || 0,
      final_url: page.url(),
      title: await page.title(),
      html: await page.content(),
      user_agent: await page.evaluate(() => navigator.userAgent),
      cookies: await portableCookies(context),
      replay: { headers: documentRequest === undefined ? {} : replayHeaders(await documentRequest.allHeaders()) },
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
