import type { Locator, Page } from "patchright";
import type { Log } from "../shared/log.ts";

const challengeTitles = ["just a moment", "one moment", "attention required", "security verification", "checking your browser"];
const challengeText = ["verify you are human", "verify your session", "performing security verification", "cloudflare ray id", "enable javascript and cookies"];
const challengeElements = "#challenge-form, form[action*='/cdn-cgi/challenge-platform'], iframe[src*='challenges.cloudflare.com'], .cf-turnstile";
const clickTargets = ["input[type=checkbox]", "[role=checkbox]", ".cf-turnstile", "[data-sitekey]"];
const maxAttempts = 3;
const clearanceWaitMs = 15_000;

export async function settleChallenge(page: Page, log: Log): Promise<void> {
  for (let attempt = 1; attempt <= maxAttempts && await challenged(page); attempt += 1) {
    const target = await clickTarget(page);
    log("challenge_detected", { attempt, title: await page.title(), target: target !== undefined });
    if (target !== undefined) await humanClick(page, target);
    for (let waited = 0; waited < clearanceWaitMs && await challenged(page); waited += 500) await page.waitForTimeout(500);
  }
  log("challenge_settled", { challenged: await challenged(page), title: await page.title(), url: page.url() });
}

async function challenged(page: Page): Promise<boolean> {
  const title = (await page.title()).toLowerCase();
  if (challengeTitles.some((value) => title.includes(value))) return true;
  const body = (await page.locator("body").innerText({ timeout: 2_000 }).catch(() => "")).toLowerCase();
  if (challengeText.some((value) => body.includes(value))) return true;
  return (await page.locator(challengeElements).count()) > 0;
}

async function clickTarget(page: Page): Promise<Locator | undefined> {
  const frames = [page.mainFrame(), ...page.frames().filter((frame) => frame !== page.mainFrame())];
  for (const frame of frames) {
    for (const selector of clickTargets) {
      const candidate = frame.locator(selector).first();
      if (await candidate.isVisible({ timeout: 500 }).catch(() => false)) return candidate;
    }
  }
  return undefined;
}

async function humanClick(page: Page, target: Locator): Promise<void> {
  const box = await target.boundingBox();
  if (box === null) return;
  const x = box.x + box.width * (0.45 + Math.random() * 0.1);
  const y = box.y + box.height * (0.45 + Math.random() * 0.1);
  await page.mouse.move(x, y, { steps: 12 + Math.floor(Math.random() * 9) });
  await page.waitForTimeout(150 + Math.floor(Math.random() * 250));
  await page.mouse.click(x, y);
}
