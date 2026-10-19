import type { Page } from "patchright";
import type { Log } from "../shared/log.ts";

const challengeFrame = "iframe[src*='captcha-delivery.com']";
const deviceCheckWaitMs = 15_000;

export async function settleDataDome(page: Page, log: Log): Promise<void> {
  if (!await challenged(page)) return;
  log("datadome_detected", { url: page.url() });
  for (let waited = 0; waited < deviceCheckWaitMs && await challenged(page); waited += 500) await page.waitForTimeout(500);
  if (await challenged(page)) {
    log("datadome_captcha_left", { url: page.url() });
    return;
  }
  await page.waitForLoadState("load", { timeout: 15_000 }).catch(() => log("datadome_reload_slow", { url: page.url() }));
  log("datadome_passed", { url: page.url() });
}

async function challenged(page: Page): Promise<boolean> {
  return (await page.locator(challengeFrame).count()) > 0;
}
