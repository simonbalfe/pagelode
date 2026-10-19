import type { Page, Request } from "patchright";

const contentResourceTypes: Record<string, true> = { document: true, script: true, xhr: true, fetch: true };
const quietWindowMs = 200;
const pollMs = 50;

export type Activity = {
  quiet(maxMs: number): Promise<void>;
  stop(): void;
};

export function trackActivity(page: Page): Activity {
  const inFlight = new Set<Request>();
  let lastChange = Date.now();

  function started(request: Request): void {
    if (contentResourceTypes[request.resourceType()] !== true) return;
    inFlight.add(request);
    lastChange = Date.now();
  }

  function ended(request: Request): void {
    if (inFlight.delete(request)) lastChange = Date.now();
  }

  page.on("request", started);
  page.on("requestfinished", ended);
  page.on("requestfailed", ended);

  return {
    async quiet(maxMs: number): Promise<void> {
      const startedAt = Date.now();
      const deadline = startedAt + maxMs;
      let mutations = await mutationCount(page);
      while (Date.now() < deadline) {
        await Bun.sleep(Math.min(pollMs, deadline - Date.now()));
        const current = await mutationCount(page);
        if (current === undefined || current !== mutations) lastChange = Date.now();
        mutations = current;
        if (inFlight.size === 0 && Date.now() - Math.max(lastChange, startedAt) >= quietWindowMs) return;
      }
    },
    stop(): void {
      page.off("request", started);
      page.off("requestfinished", ended);
      page.off("requestfailed", ended);
    },
  };
}

async function mutationCount(page: Page): Promise<number | undefined> {
  return await page.evaluate(() => {
    const key = "pagelodeMutations";
    const count = (): number => {
      const value: unknown = Reflect.get(globalThis, key);
      return typeof value === "number" ? value : 0;
    };
    if (typeof Reflect.get(globalThis, key) !== "number") {
      Reflect.set(globalThis, key, 0);
      new MutationObserver((records) => {
        Reflect.set(globalThis, key, count() + records.length);
      }).observe(document, { subtree: true, childList: true, characterData: true });
    }
    return count();
  }).catch(() => undefined);
}
