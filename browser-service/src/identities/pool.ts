import { createHash } from "node:crypto";
import { mkdir, readlink, rm } from "node:fs/promises";
import { hostname } from "node:os";
import { join } from "node:path";
import type { BrowserContext } from "patchright";
import { launchBrowser } from "../browser/launch.ts";
import type { Persona } from "../browser/persona.ts";
import type { Config } from "../config.ts";
import { portableCookies } from "../render/replay.ts";
import { ServiceError } from "../shared/errors.ts";
import type { Log } from "../shared/log.ts";
import type { Cookie } from "../shared/schemas.ts";

const closeTimeoutMs = 15_000;
const killWaitMs = 5_000;

type Browser = {
  readonly context: BrowserContext;
  readonly vacate: () => void;
  users: number;
  retired: boolean;
  closed: Promise<void> | undefined;
};

type Identity = {
  readonly key: string;
  readonly directory: string;
  readonly proxyURL: string | undefined;
  readonly createdAt: Date;
  browser: Browser | undefined;
  launching: Promise<void> | undefined;
  persona: Persona | undefined;
  headers: Record<string, string>;
  users: number;
  renders: number;
  lastUsed: number;
};

export type Lease = {
  readonly proxyURL: string | undefined;
  context(signal: AbortSignal): Promise<BrowserContext>;
  recordHeaders(headers: Record<string, string>): void;
  retire(): void;
  release(): void;
};

export type IdentityView = {
  key: string;
  proxy: string;
  ip?: string;
  country?: string;
  timezone?: string;
  languages: ReadonlyArray<string>;
  renders: number;
  active: number;
  created_at: string;
  last_used_at: string;
};

export type Clearance = {
  identity: string;
  proxy_url?: string;
  headers: Record<string, string>;
  cookies: ReadonlyArray<Cookie>;
};

export class Identities {
  readonly #config: Config;
  readonly #log: Log;
  readonly #identities = new Map<string, Identity>();
  readonly #profiles = new Map<string, Promise<void>>();

  constructor(config: Config, log: Log) {
    this.#config = config;
    this.#log = log;
  }

  acquire(key: string, proxyURL: string | undefined): Lease {
    let identity = this.#identities.get(key);
    if (identity !== undefined && identity.proxyURL !== proxyURL) {
      throw new ServiceError(409, "identity_conflict", `identity ${JSON.stringify(key)} is bound to a different proxy; rotate it first`);
    }
    if (identity === undefined) {
      this.#evictIdle();
      identity = this.#create(key, proxyURL);
      this.#identities.set(key, identity);
    }
    const leased = identity;
    leased.users += 1;
    leased.renders += 1;
    let attached: Browser | undefined;
    let released = false;
    return {
      proxyURL: leased.proxyURL,
      context: async (signal) => {
        attached = await this.#attach(leased, signal);
        return attached.context;
      },
      recordHeaders: (headers) => {
        if (Object.keys(headers).length > 0) leased.headers = headers;
      },
      retire: () => {
        if (attached !== undefined) this.#retire(leased, attached, "wedged");
      },
      release: () => {
        if (released) return;
        released = true;
        leased.users -= 1;
        leased.lastUsed = Date.now();
        if (attached === undefined) return;
        attached.users -= 1;
        if (attached.retired && attached.users === 0) void this.#shutdown(leased, attached);
      },
    };
  }

  list(): ReadonlyArray<IdentityView> {
    return [...this.#identities.values()].map((identity) => ({
      key: identity.key,
      proxy: identity.proxyURL === undefined ? "direct" : new URL(identity.proxyURL).host,
      ...(identity.persona?.ip === undefined ? {} : { ip: identity.persona.ip }),
      ...(identity.persona?.country === undefined ? {} : { country: identity.persona.country }),
      ...(identity.persona?.timezone === undefined ? {} : { timezone: identity.persona.timezone }),
      languages: identity.persona?.languages ?? [],
      renders: identity.renders,
      active: identity.users,
      created_at: identity.createdAt.toISOString(),
      last_used_at: new Date(identity.lastUsed).toISOString(),
    }));
  }

  async clearance(key: string, url: string): Promise<Clearance> {
    const identity = this.#identities.get(key);
    const context = identity?.browser?.context;
    if (identity === undefined || context === undefined) throw new ServiceError(404, "not_found", `identity ${JSON.stringify(key)} is not warm`);
    return {
      identity: key,
      ...(identity.proxyURL === undefined ? {} : { proxy_url: identity.proxyURL }),
      headers: identity.headers,
      cookies: await portableCookies(context, url),
    };
  }

  async rotate(key: string): Promise<void> {
    const identity = this.#identities.get(key);
    if (identity === undefined) return;
    if (identity.users > 0) throw new ServiceError(409, "identity_conflict", `identity ${JSON.stringify(key)} has renders in progress`);
    this.#identities.delete(key);
    if (identity.browser !== undefined) this.#retire(identity, identity.browser, "rotated");
    const { previous, vacate } = this.#occupy(identity.directory);
    try {
      await previous;
      await rm(identity.directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
    } finally {
      vacate();
    }
    this.#log("identity_rotated", { key });
  }

  async close(): Promise<void> {
    const identities = [...this.#identities.values()];
    this.#identities.clear();
    for (const identity of identities) {
      const { browser } = identity;
      if (browser === undefined) continue;
      this.#retire(identity, browser, "shutdown");
      void this.#shutdown(identity, browser);
    }
    await Promise.all(this.#profiles.values());
  }

  #create(key: string, proxyURL: string | undefined): Identity {
    return {
      key,
      directory: join(this.#config.dataDirectory, `identity-${createHash("sha256").update(key).digest("hex").slice(0, 16)}`),
      proxyURL,
      createdAt: new Date(),
      browser: undefined,
      launching: undefined,
      persona: undefined,
      headers: {},
      users: 0,
      renders: 0,
      lastUsed: Date.now(),
    };
  }

  async #attach(identity: Identity, signal: AbortSignal): Promise<Browser> {
    for (;;) {
      signal.throwIfAborted();
      const current = identity.browser;
      if (current !== undefined) {
        current.users += 1;
        return current;
      }
      await abortable(identity.launching ?? this.#startLaunch(identity), signal);
    }
  }

  #startLaunch(identity: Identity): Promise<void> {
    const launching: Promise<void> = this.#launch(identity).finally(() => {
      if (identity.launching === launching) identity.launching = undefined;
    });
    identity.launching = launching;
    return launching;
  }

  async #launch(identity: Identity): Promise<void> {
    const { key, directory, proxyURL } = identity;
    const { previous, vacate } = this.#occupy(directory);
    await previous;
    let context: BrowserContext;
    try {
      await mkdir(directory, { recursive: true });
      const launched = await launchBrowser(this.#config, this.#log, { directory, proxyURL, headless: this.#config.headless });
      context = launched.context;
      identity.persona = launched.persona;
    } catch (error: unknown) {
      vacate();
      const message = error instanceof Error ? error.message : String(error);
      this.#log("identity_launch_failed", { key, message });
      throw new ServiceError(502, "render_failed", `browser launch for identity ${JSON.stringify(key)} failed: ${message}`);
    }
    const browser: Browser = { context, vacate, users: 0, retired: false, closed: undefined };
    context.once("close", () => this.#retire(identity, browser, "closed"));
    this.#log("identity_launch", { key, country: identity.persona?.country, timezone: identity.persona?.timezone, languages: identity.persona?.languages, proxy: proxyURL !== undefined });
    if (this.#identities.get(key) === identity) identity.browser = browser;
    else this.#retire(identity, browser, "dropped");
  }

  #occupy(directory: string): { previous: Promise<void>; vacate: () => void } {
    const previous = this.#profiles.get(directory) ?? Promise.resolve();
    const { promise, resolve } = Promise.withResolvers<void>();
    const free = previous.then(() => promise);
    this.#profiles.set(directory, free);
    void free.then(() => {
      if (this.#profiles.get(directory) === free) this.#profiles.delete(directory);
    });
    return { previous, vacate: resolve };
  }

  #retire(identity: Identity, browser: Browser, reason: string): void {
    if (browser.retired) return;
    browser.retired = true;
    if (identity.browser === browser) identity.browser = undefined;
    this.#log("identity_retired", { key: identity.key, reason, users: browser.users });
    if (browser.users === 0) void this.#shutdown(identity, browser);
  }

  #shutdown(identity: Identity, browser: Browser): Promise<void> {
    browser.closed ??= this.#close(identity, browser);
    return browser.closed;
  }

  async #close(identity: Identity, browser: Browser): Promise<void> {
    const closed = browser.context.close().then(() => true, () => true);
    if (!await Promise.race([closed, Bun.sleep(closeTimeoutMs).then(() => false)])) {
      this.#log("identity_close_timeout", { key: identity.key });
      await killProfileOwner(identity.directory);
      await Promise.race([closed, Bun.sleep(killWaitMs)]);
    }
    browser.vacate();
  }

  #evictIdle(): void {
    if (this.#identities.size < this.#config.maxIdentities) return;
    let oldest: Identity | undefined;
    for (const identity of this.#identities.values()) {
      if (identity.users === 0 && (oldest === undefined || identity.lastUsed < oldest.lastUsed)) oldest = identity;
    }
    if (oldest === undefined) return;
    this.#identities.delete(oldest.key);
    if (oldest.browser !== undefined) this.#retire(oldest, oldest.browser, "evicted");
  }
}

async function abortable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  const { promise: aborted, reject } = Promise.withResolvers<never>();
  const abort = (): void => reject(signal.reason);
  signal.addEventListener("abort", abort, { once: true });
  try {
    return await Promise.race([promise, aborted]);
  } finally {
    signal.removeEventListener("abort", abort);
  }
}

async function killProfileOwner(directory: string): Promise<void> {
  const owner = await readlink(join(directory, "SingletonLock")).catch(() => "");
  const separator = owner.lastIndexOf("-");
  const pid = Number(owner.slice(separator + 1));
  if (separator <= 0 || owner.slice(0, separator) !== hostname() || !Number.isInteger(pid) || pid <= 0) return;
  try {
    process.kill(-pid, "SIGKILL");
  } catch {
    try {
      process.kill(pid, "SIGKILL");
    } catch {
      return;
    }
  }
}
