import type { Locator, Page } from "patchright";
import signatures from "../../../signatures/signatures.json" with { type: "json" };
import type { Log } from "../shared/log.ts";
import { findDataDome } from "./datadome.ts";
import type { Challenge } from "./schema.ts";

const signals = signatures.challenge;
const challengeSelectors = signals.selectors.join(", ");
const clickTargets = ["input[type=checkbox]", "[role=checkbox]", ".cf-turnstile", "[data-sitekey]"];
const maxAttempts = 3;
const clearanceWaitMs = 15_000;

export async function settleChallenge(page: Page, documentHeaders: () => Record<string, string>, log: Log): Promise<void> {
  for (let attempt = 1; attempt <= maxAttempts && await challenged(page, documentHeaders()); attempt += 1) {
    const target = await clickTarget(page);
    log("challenge_detected", { attempt, title: await page.title(), target: target !== undefined });
    if (target !== undefined) await humanClick(page, target);
    for (let waited = 0; waited < clearanceWaitMs && await challenged(page, documentHeaders()); waited += 500) await page.waitForTimeout(500);
  }
  log("challenge_settled", { challenged: await challenged(page, documentHeaders()), title: await page.title(), url: page.url() });
}

export async function remainingChallenge(page: Page, documentHeaders: Record<string, string>): Promise<Challenge | undefined> {
  const dataDome = await findDataDome(page);
  if (dataDome !== undefined) return { vendor: "datadome", ...dataDome };
  const widget = page.locator(signatures.captchas.recaptcha_widget).first();
  if (await widget.count() > 0) {
    const [sitekey, dataS] = await Promise.all(["data-sitekey", "data-s"].map((name) => widget.getAttribute(name, { timeout: 1_000 }).catch(() => null)));
    return { vendor: "recaptcha", ...(typeof sitekey === "string" ? { sitekey } : {}), ...(typeof dataS === "string" ? { data_s: dataS } : {}) };
  }
  if (await challenged(page, documentHeaders)) return { vendor: "cloudflare" };
  return undefined;
}

export async function challengeHinted(page: Page, status: number, documentHeaders: Record<string, string>): Promise<boolean> {
  if (signals.hint_statuses.includes(status)) return true;
  const names = Object.keys(documentHeaders).map((name) => name.toLowerCase());
  if (signals.hint_headers.some((hint) => names.includes(hint))) return true;
  return await headerOrPageSignal(page, documentHeaders);
}

async function challenged(page: Page, documentHeaders: Record<string, string>): Promise<boolean> {
  if (await headerOrPageSignal(page, documentHeaders)) return true;
  const body = (await page.locator("body").innerText({ timeout: 2_000 }).catch(() => "")).toLowerCase();
  if (signals.text.some((value) => body.includes(value))) return true;
  return (await page.locator(challengeSelectors).count()) > 0;
}

async function headerOrPageSignal(page: Page, documentHeaders: Record<string, string>): Promise<boolean> {
  const headers = Object.entries(documentHeaders).map(([name, value]) => [name.toLowerCase(), value] as const);
  if (signals.headers.some((signal) => headers.some(([name, value]) => name === signal.name.toLowerCase() && value.includes(signal.value)))) return true;
  const url = page.url();
  if (signals.url_contains.some((value) => url.includes(value))) return true;
  const title = (await page.title()).toLowerCase();
  return signals.titles.some((value) => title.includes(value));
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
