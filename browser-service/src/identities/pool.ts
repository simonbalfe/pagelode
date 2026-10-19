import { createHash } from "node:crypto";
import { mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import type { BrowserContext } from "patchright";
import { launchBrowser, type LaunchedBrowser } from "../browser/launch.ts";
import type { Persona } from "../browser/persona.ts";
import type { Config } from "../config.ts";
import { portableCookies } from "../render/replay.ts";
import { ServiceError } from "../shared/errors.ts";
import type { Log } from "../shared/log.ts";
import type { Cookie } from "../shared/schemas.ts";

type Identity = {
  readonly key: string;
  readonly directory: string;
  readonly proxyURL: string | undefined;
  readonly createdAt: Date;
  readonly context: Promise<BrowserContext>;
  persona: Persona | undefined;
  headers: Record<string, string>;
  users: number;
  renders: number;
  lastUsed: number;
};

export type Lease = {
  readonly context: Promise<BrowserContext>;
  readonly proxyURL: string | undefined;
  release(): void;
  recordHeaders(headers: Record<string, string>): void;
  discard(): void;
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
    return {
      context: leased.context,
      proxyURL: leased.proxyURL,
      release: () => {
        leased.users -= 1;
        leased.lastUsed = Date.now();
      },
      recordHeaders: (headers) => {
        if (Object.keys(headers).length > 0) leased.headers = headers;
      },
      discard: () => {
        if (this.#identities.get(key) === leased) this.#identities.delete(key);
        this.#log("identity_discarded", { key });
        void leased.context.then((context) => context.close()).catch(() => this.#log("identity_close_failed", { key }));
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
    if (identity === undefined) throw new ServiceError(404, "not_found", `identity ${JSON.stringify(key)} is not warm`);
    return {
      identity: key,
      ...(identity.proxyURL === undefined ? {} : { proxy_url: identity.proxyURL }),
      headers: identity.headers,
      cookies: await portableCookies(await identity.context, url),
    };
  }

  async rotate(key: string): Promise<void> {
    const identity = this.#identities.get(key);
    if (identity === undefined) return;
    if (identity.users > 0) throw new ServiceError(409, "identity_conflict", `identity ${JSON.stringify(key)} has renders in progress`);
    this.#identities.delete(key);
    await (await identity.context.catch(() => undefined))?.close();
    await rm(identity.directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
    this.#log("identity_rotated", { key });
  }

  async close(): Promise<void> {
    const contexts = [...this.#identities.values()].map((identity) => identity.context);
    this.#identities.clear();
    await Promise.all(contexts.map(async (context) => (await context.catch(() => undefined))?.close()));
  }

  #create(key: string, proxyURL: string | undefined): Identity {
    const directory = join(this.#config.dataDirectory, `identity-${createHash("sha256").update(key).digest("hex").slice(0, 16)}`);
    const identity: Identity = {
      key,
      directory,
      proxyURL,
      createdAt: new Date(),
      context: this.#launch(key, directory, proxyURL).then(({ context, persona }) => {
        identity.persona = persona;
        return context;
      }),
      persona: undefined,
      headers: {},
      users: 0,
      renders: 0,
      lastUsed: Date.now(),
    };
    return identity;
  }

  async #launch(key: string, directory: string, proxyURL: string | undefined): Promise<LaunchedBrowser> {
    const forget = (): void => {
      if (this.#identities.get(key)?.directory === directory) this.#identities.delete(key);
    };
    try {
      await mkdir(directory, { recursive: true });
      const launched = await launchBrowser(this.#config, this.#log, { directory, proxyURL, headless: this.#config.headless });
      const { persona } = launched;
      this.#log("identity_launch", { key, country: persona.country, timezone: persona.timezone, languages: persona.languages, proxy: proxyURL !== undefined });
      launched.context.once("close", forget);
      return launched;
    } catch (error: unknown) {
      forget();
      throw error;
    }
  }

  #evictIdle(): void {
    if (this.#identities.size < this.#config.maxIdentities) return;
    let oldest: Identity | undefined;
    for (const identity of this.#identities.values()) {
      if (identity.users === 0 && (oldest === undefined || identity.lastUsed < oldest.lastUsed)) oldest = identity;
    }
    if (oldest === undefined) return;
    this.#identities.delete(oldest.key);
    void oldest.context.then((context) => context.close()).catch(() => this.#log("identity_close_failed"));
  }
}
