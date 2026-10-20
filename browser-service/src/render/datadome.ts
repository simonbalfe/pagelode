import type { Page } from "patchright";
import signatures from "../../../pkg/fetch/signatures.json" with { type: "json" };
import type { Log } from "../shared/log.ts";

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

export async function findDataDome(page: Page): Promise<{ url?: string } | undefined> {
  for (const frame of page.frames()) {
    const iframe = frame.locator(signatures.captchas.datadome_frame).first();
    if (await iframe.count().catch(() => 0) === 0) continue;
    const url = await iframe.getAttribute("src", { timeout: 1_000 }).catch(() => null);
    return url === null ? {} : { url };
  }
  return undefined;
}

async function challenged(page: Page): Promise<boolean> {
  return await findDataDome(page) !== undefined;
}
