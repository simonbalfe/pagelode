import type { Page } from "patchright";
import type { Log } from "../shared/log.ts";
import type { ExploreOptions } from "./schema.ts";

const maxScrollSteps = 20;
const scrollWaitMs = 250;
const scrollStepFraction = 0.9;
const maxExploredURLs = 200;
const maxMoreLabelLength = 60;
const clickSettleMs = 1_000;
const maxPickAttempts = 100;
const deadlineReserveMs = 2_000;
const navigationTimeoutMs = 10_000;
const unsafePattern = "log ?out|sign ?out|delete|remove|buy|checkout|purchase|pay|subscribe|unsubscribe|cancel|order|submit|confirm|donate";
const morePattern = "load more|show more|view more|see more|next|more results|older|expand";
const toggleSelector = [
  "[aria-expanded=\"false\"]",
  "summary",
  "[role=\"tab\"]",
  "[data-toggle=\"collapse\"]",
  "[data-toggle=\"dropdown\"]",
  "[data-bs-toggle=\"collapse\"]",
  "[data-bs-toggle=\"dropdown\"]",
  ".navbar-toggler",
  ".menu-toggle",
  ".nav-toggle",
  ".hamburger",
  "nav [aria-haspopup]:not([aria-haspopup=\"false\"])",
  "header [aria-haspopup]:not([aria-haspopup=\"false\"])",
  "[role=\"navigation\"] [aria-haspopup]:not([aria-haspopup=\"false\"])",
  "[role=\"menubar\"] [aria-haspopup]:not([aria-haspopup=\"false\"])",
].join(", ");

export type ExploreEnvironment = {
  readonly deadline: number;
  readonly quiet: (maxMs: number) => Promise<void>;
  readonly log: Log;
};

type Rect = { left: number; top: number; width: number; height: number };
type Pick = { key: string; elements: number };
type PickArguments = {
  unsafe: string;
  more: string;
  toggles: string;
  maxMoreLabel: number;
  clicked: Record<string, number>;
  stash: string;
};

const blockedClick = Number.MAX_SAFE_INTEGER;
const targetStash = "pagelodeExploreTarget";

export async function explorePage(page: Page, options: ExploreOptions, environment: ExploreEnvironment): Promise<string[]> {
  const { deadline, quiet, log } = environment;
  const start = page.url();
  const origin = new URL(start).origin;
  const explored = new Set<string>();
  const clicked: Record<string, number> = {};
  const closePopup = (popup: Page): void => {
    void popup.close().catch(() => log("explore_popup_close_failed"));
  };
  const budgetLeft = (): boolean => Date.now() < deadline - deadlineReserveMs;
  page.on("popup", closePopup);
  try {
    await scrollToLoad(page, budgetLeft);
    let clicks = 0;
    for (let attempts = 0; clicks < options.max_clicks && attempts < maxPickAttempts && budgetLeft(); attempts += 1) {
      const pick = await pickTarget(page, clicked);
      if (pick === undefined || pick === null) break;
      clicked[pick.key] = pick.elements;
      if (!(await clickStashedTarget(page))) {
        clicked[pick.key] = blockedClick;
        log("explore_target_blocked", { key: pick.key });
        continue;
      }
      clicks += 1;
      log("explore_clicked", { key: pick.key });
      await quiet(clickSettleMs);
      const current = page.url();
      if (current === start) continue;
      if (sameOrigin(current, origin) && explored.size < maxExploredURLs) explored.add(current);
      log("explore_url_changed", { from: start, to: current });
      if (!(await returnToStart(page, start, deadline))) break;
      await quiet(clickSettleMs);
    }
    log("explore_complete", { clicks, explored: explored.size });
  } finally {
    page.off("popup", closePopup);
  }
  return [...explored];
}

async function scrollToLoad(page: Page, budgetLeft: () => boolean): Promise<void> {
  for (let step = 0; step < maxScrollSteps && budgetLeft(); step += 1) {
    const before = await page.evaluate((fraction) => {
      const height = document.documentElement.scrollHeight;
      window.scrollBy(0, Math.round(window.innerHeight * fraction));
      return height;
    }, scrollStepFraction).catch(() => undefined);
    if (before === undefined) return;
    await Bun.sleep(scrollWaitMs);
    const after = await page.evaluate(() => ({
      height: document.documentElement.scrollHeight,
      atBottom: window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2,
    })).catch(() => undefined);
    if (after === undefined || (after.height <= before && after.atBottom)) return;
  }
}

async function pickTarget(page: Page, clicked: Record<string, number>): Promise<Pick | null | undefined> {
  const args: PickArguments = { unsafe: unsafePattern, more: morePattern, toggles: toggleSelector, maxMoreLabel: maxMoreLabelLength, clicked, stash: targetStash };
  return await page.evaluate(selectSafeTarget, args).catch(() => undefined);
}

function selectSafeTarget(args: PickArguments): Pick | null {
  const unsafe = new RegExp(args.unsafe, "i");
  const more = new RegExp(args.more, "i");
  const elements = document.querySelectorAll("*").length;
  const clean = (value: string | null | undefined): string => (value ?? "").replace(/\s+/g, " ").trim();
  const pathOf = (element: Element): string => {
    const parts: string[] = [];
    for (let node: Element | null = element; node !== null && node !== document.documentElement; node = node.parentElement) {
      let index = 1;
      for (let sibling = node.previousElementSibling; sibling !== null; sibling = sibling.previousElementSibling) {
        if (sibling.tagName === node.tagName) index += 1;
      }
      parts.push(`${node.tagName.toLowerCase()}:${index}`);
    }
    return parts.reverse().join(">");
  };
  const linkAllowed = (link: HTMLAnchorElement): boolean => {
    const target = link.target.toLowerCase();
    if (link.hasAttribute("download") || (target !== "" && target !== "_self")) return false;
    if (link.protocol === "javascript:") return true;
    return (link.protocol === "http:" || link.protocol === "https:") && link.origin === location.origin;
  };
  const navigates = (link: HTMLAnchorElement | null): boolean => {
    if (link === null || link.protocol === "javascript:") return false;
    const without = (value: string): string => value.split("#")[0] ?? "";
    return without(link.href) !== without(location.href);
  };
  const registryName = `${args.stash}Clicked`;
  const existingRegistry: unknown = Reflect.get(globalThis, registryName);
  const registry = existingRegistry instanceof WeakMap ? existingRegistry as WeakMap<Element, number> : new WeakMap<Element, number>();
  Reflect.set(globalThis, registryName, registry);
  const eligible = (element: Element, key: string, wantsMore: boolean): boolean => {
    if (args.clicked[key] === Number.MAX_SAFE_INTEGER) return false;
    const previous = registry.get(element) ?? args.clicked[key];
    return previous === undefined || (wantsMore && elements > previous);
  };

  const candidates: { element: Element; key: string; priority: number; wantsMore: boolean }[] = [];
  const selector = `${args.toggles}, button, a`;
  for (const element of document.querySelectorAll(selector)) {
    const tag = element.tagName.toLowerCase();
    if (tag === "input" || tag === "select" || tag === "textarea" || tag === "option" || tag === "form") continue;
    if (element.closest("form, [role=\"form\"], [inert], [hidden]") !== null) continue;
    if (element.querySelector("input, select, textarea, form") !== null) continue;
    if (element.matches(":disabled, [aria-disabled=\"true\"]")) continue;
    if (element.getAttribute("aria-expanded") === "true" || element.getAttribute("aria-selected") === "true") continue;
    if (element.matches("summary") && element.parentElement?.hasAttribute("open") === true) continue;

    const label = clean(`${clean(element.getAttribute("aria-label"))} ${clean(element.textContent)}`);
    const link = element.closest<HTMLAnchorElement>("a[href]");
    const checked = [label, clean(element.getAttribute("title")), clean(element.getAttribute("value")), link === null ? "" : `${link.getAttribute("href") ?? ""} ${link.href} ${clean(link.textContent)}`].join(" ");
    if (unsafe.test(checked)) continue;
    if (link !== null && !linkAllowed(link)) continue;

    const isToggle = element.matches(args.toggles);
    const isNext = element.matches("a[rel~=\"next\"]");
    const isMoreControl = (tag === "button" || tag === "a" || element.getAttribute("role") === "button") && label.length > 0 && label.length <= args.maxMoreLabel && more.test(label);
    if (!isToggle && !isNext && !isMoreControl) continue;

    const key = `${pathOf(element)}|${label.slice(0, 40)}`;
    const wantsMore = !isToggle;
    if (!eligible(element, key, wantsMore)) continue;
    const priority = navigates(link) ? 2 : isToggle ? 0 : 1;
    candidates.push({ element, key, priority, wantsMore });
  }
  candidates.sort((a, b) => a.priority - b.priority);

  const first = candidates.find(({ element }) => element.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }) && element.getClientRects().length > 0 && getComputedStyle(element).pointerEvents !== "none");
  if (first === undefined) return null;
  registry.set(first.element, elements);
  Reflect.set(globalThis, args.stash, first.element);
  return { key: first.key, elements };
}

async function measureStashedTarget(stash: string): Promise<Rect | null> {
  const element: unknown = Reflect.get(globalThis, stash);
  if (!(element instanceof Element)) return null;
  const frames = (): Promise<void> => {
    const { promise, resolve } = Promise.withResolvers<void>();
    setTimeout(resolve, 100);
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
    return promise;
  };
  let previous: DOMRect | undefined;
  for (let attempt = 0; attempt < 8; attempt += 1) {
    if (!element.isConnected) return null;
    element.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
    await frames();
    const rect = element.getBoundingClientRect();
    const settled = previous !== undefined && Math.abs(previous.top - rect.top) < 1 && Math.abs(previous.left - rect.left) < 1;
    previous = rect;
    if (!settled) continue;
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    if (rect.width <= 0 || rect.height <= 0) return null;
    if (x < 0 || y < 0 || x >= window.innerWidth || y >= window.innerHeight) continue;
    const hit = document.elementFromPoint(x, y);
    if (hit === null || (hit !== element && !element.contains(hit))) return null;
    return { left: rect.left, top: rect.top, width: rect.width, height: rect.height };
  }
  return null;
}

async function clickStashedTarget(page: Page): Promise<boolean> {
  const approach = await page.evaluate(measureStashedTarget, targetStash).catch(() => null);
  if (approach === null) return false;
  await page.mouse.move(approach.left + approach.width * 0.5, approach.top + approach.height * 0.5, { steps: 6 + Math.floor(Math.random() * 7) });
  await Bun.sleep(100 + Math.floor(Math.random() * 150));
  const rect = await page.evaluate(measureStashedTarget, targetStash).catch(() => null);
  if (rect === null) return false;
  const x = rect.left + rect.width * (0.45 + Math.random() * 0.1);
  const y = rect.top + rect.height * (0.45 + Math.random() * 0.1);
  await page.mouse.move(x, y, { steps: 2 });
  await page.mouse.click(x, y);
  return true;
}

async function returnToStart(page: Page, start: string, deadline: number): Promise<boolean> {
  const timeout = (): number => Math.min(navigationTimeoutMs, Math.max(1_000, deadline - Date.now()));
  await page.goBack({ timeout: timeout(), waitUntil: "commit" }).catch(() => null);
  if (page.url() !== start) await page.goto(start, { timeout: timeout(), waitUntil: "domcontentloaded" }).catch(() => null);
  return page.url() === start;
}

function sameOrigin(value: string, origin: string): boolean {
  try {
    return new URL(value).origin === origin;
  } catch {
    return false;
  }
}
