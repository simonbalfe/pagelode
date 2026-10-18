import type { Page } from "patchright";
import type { CapSolverSettings } from "../config.ts";
import type { Log } from "../shared/log.ts";
import { solveRecaptchaV2 } from "./capsolver.ts";

const recaptchaWidget = ".g-recaptcha[data-sitekey]";

export type GoogleSorryContext = {
  readonly proxyURL: string | undefined;
  readonly capsolver: CapSolverSettings | undefined;
  readonly signal: AbortSignal;
  readonly log: Log;
};

export async function clearGoogleSorry(page: Page, context: GoogleSorryContext): Promise<void> {
  const { log } = context;
  const location = new URL(page.url());
  if (!location.hostname.startsWith("www.google.") || !location.pathname.startsWith("/sorry")) return;
  log("google_sorry_detected", { url: location.origin + location.pathname });
  if (context.capsolver === undefined) {
    log("google_sorry_unsolved", { reason: "no CapSolver key" });
    return;
  }
  const widget = page.locator(recaptchaWidget).first();
  const websiteKey = await widget.getAttribute("data-sitekey", { timeout: 5_000 });
  if (websiteKey === null) return;
  const token = await solveRecaptchaV2(context.capsolver, {
    websiteURL: page.url(),
    websiteKey,
    dataS: await widget.getAttribute("data-s") ?? undefined,
    proxyURL: context.proxyURL,
  }, context.signal);
  await Promise.all([
    page.waitForURL((url) => !url.pathname.startsWith("/sorry"), { timeout: 30_000 }),
    page.evaluate((value) => {
      const response = document.querySelector<HTMLTextAreaElement>("textarea[name='g-recaptcha-response']");
      const form = response?.closest("form") ?? document.querySelector("form");
      if (form === null) throw new Error("Google captcha form not found");
      if (response !== null) response.value = value;
      else form.append(Object.assign(document.createElement("input"), { type: "hidden", name: "g-recaptcha-response", value }));
      form.submit();
    }, token),
  ]);
  log("google_sorry_solved", { url: page.url() });
}
