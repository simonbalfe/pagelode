import { errors, type BrowserContext, type Page, type Request } from "patchright";
import type { Log } from "../shared/log.ts";
import { trackActivity } from "./activity.ts";
import { captureTraffic } from "./capture.ts";
import { challengeHinted, remainingChallenge, settleChallenge } from "./challenge.ts";
import { settleDataDome } from "./datadome.ts";
import { explorePage } from "./explore.ts";
import { portableCookies, replayHeaders } from "./replay.ts";
import type { RenderRequest, RenderResult } from "./schema.ts";

const blockedResourceTypes: Record<string, true> = { image: true, media: true, font: true };

export type RenderEnvironment = {
  readonly signal: AbortSignal;
  readonly log: Log;
};

export async function renderPage(context: BrowserContext, input: RenderRequest, environment: RenderEnvironment): Promise<RenderResult> {
  const { signal, log } = environment;
  const page = await context.newPage();
  const closePage = (): void => {
    void page.close().catch(() => log("page_close_failed"));
  };
  signal.addEventListener("abort", closePage, { once: true });
  if (signal.aborted) closePage();
  const captureDeadline = input.capture === undefined ? undefined : setTimeout(closePage, input.timeout_ms);
  const deadline = Date.now() + input.timeout_ms;
  const activity = trackActivity(page);
  try {
    const capture = input.capture === undefined ? undefined : captureTraffic(page, input.capture);
    let documentStatus = 0;
    let documentHeaders: Record<string, string> = {};
    let documentRequest: Request | undefined;
    let exploring = false;
    page.on("response", (response) => {
      if (exploring || response.request().resourceType() !== "document" || response.frame() !== page.mainFrame()) return;
      documentStatus = response.status();
      documentHeaders = response.headers();
      documentRequest = response.request();
    });
    if (input.block_resources) {
      await page.route("**/*", (route) => {
        const settled = blockedResourceTypes[route.request().resourceType()] ? route.abort() : route.continue();
        settled.catch(() => undefined);
      });
    }
    if (input.cookies.length > 0) await addCookies(page, input);
    const response = await page.goto(input.url, { timeout: input.timeout_ms, waitUntil: input.wait_until });
    const hinted = await challengeHinted(page, documentStatus || response?.status() || 0, documentHeaders);
    log("navigation_complete", { status: response?.status() ?? 0, url: page.url(), challenge_hint: hinted });
    if (hinted && input.settle_challenge) await settleChallenge(page, () => documentHeaders, log);
    if (hinted && input.solve_captcha) await settleDataDome(page, log);
    const waitForMatched = input.wait_for === undefined ? undefined : await waitForSelector(page, input.wait_for);
    await activity.quiet(input.capture?.wait_ms ?? input.settle_ms);
    const challenge = hinted ? await remainingChallenge(page, documentHeaders) : undefined;
    exploring = true;
    const exploredURLs = input.explore === undefined ? undefined : challenge === undefined ? await explorePage(page, input.explore, { deadline, quiet: (maxMs) => activity.quiet(maxMs), log }) : [];
    return {
      status_code: documentStatus || response?.status() || 0,
      final_url: page.url(),
      title: await page.title(),
      html: await page.content(),
      user_agent: await page.evaluate(() => navigator.userAgent),
      headers: documentHeaders,
      cookies: await portableCookies(context),
      replay: { headers: documentRequest === undefined ? {} : replayHeaders(await documentRequest.allHeaders()) },
      ...(challenge === undefined ? {} : { challenge }),
      ...(waitForMatched === undefined ? {} : { wait_for_matched: waitForMatched }),
      ...(capture === undefined ? {} : { traffic: await capture.finish() }),
      ...(exploredURLs === undefined ? {} : { explored_urls: exploredURLs }),
    };
  } finally {
    activity.stop();
    clearTimeout(captureDeadline);
    signal.removeEventListener("abort", closePage);
    await page.close();
  }
}

async function waitForSelector(page: Page, waitFor: NonNullable<RenderRequest["wait_for"]>): Promise<boolean> {
  try {
    await page.waitForSelector(waitFor.selector, { state: "attached", timeout: waitFor.timeout_ms });
    return true;
  } catch (error: unknown) {
    if (error instanceof errors.TimeoutError) return false;
    throw error;
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
