import type { Page } from "patchright";
import type { CapSolverSettings } from "../config.ts";
import type { Log } from "../shared/log.ts";
import { solveDataDome } from "./capsolver.ts";

const challengeFrame = "iframe[src*='captcha-delivery.com']";
const deviceCheckWaitMs = 15_000;

export type DataDomeContext = {
  readonly proxyURL: string | undefined;
  readonly capsolver: CapSolverSettings | undefined;
  readonly signal: AbortSignal;
  readonly log: Log;
};

export async function clearDataDome(page: Page, context: DataDomeContext): Promise<void> {
  const { log } = context;
  if (!await challenged(page)) return;
  log("datadome_detected", { url: page.url() });
  for (let waited = 0; waited < deviceCheckWaitMs && await challenged(page); waited += 500) await page.waitForTimeout(500);
  if (!await challenged(page)) {
    await page.waitForLoadState("load", { timeout: 15_000 }).catch(() => log("datadome_reload_slow", { url: page.url() }));
    log("datadome_passed", { url: page.url() });
    return;
  }
  const captchaUrl = await page.locator(challengeFrame).first().getAttribute("src");
  if (captchaUrl === null) return;
  if (new URL(captchaUrl).searchParams.get("t") === "bv") {
    log("datadome_ip_banned", { url: page.url() });
    return;
  }
  if (context.capsolver === undefined || context.proxyURL === undefined) {
    log("datadome_unsolved", { reason: context.capsolver === undefined ? "no CapSolver key" : "no proxy" });
    return;
  }
  const cookie = await solveDataDome(context.capsolver, {
    captchaUrl,
    userAgent: await page.evaluate(() => navigator.userAgent),
    proxyURL: context.proxyURL,
  }, context.signal);
  const pair = cookie.split(";")[0] ?? "";
  const separator = pair.indexOf("=");
  await page.context().addCookies([{ name: pair.slice(0, separator), value: pair.slice(separator + 1), url: page.url() }]);
  await page.reload({ waitUntil: "domcontentloaded" });
  log("datadome_solved", { cleared: !await challenged(page), url: page.url() });
}

async function challenged(page: Page): Promise<boolean> {
  return (await page.locator(challengeFrame).count()) > 0;
}
