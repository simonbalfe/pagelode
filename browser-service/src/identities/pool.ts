import { createHash } from "node:crypto";
import { mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import type { BrowserContext } from "patchright";
import { launchBrowser, type LaunchedBrowser } from "../browser/launch.ts";
import { withProxySession, type Persona } from "../browser/persona.ts";
import type { Config } from "../config.ts";
import { ServiceError } from "../shared/errors.ts";
import type { Log } from "../shared/log.ts";

type Identity = {
  readonly key: string;
  readonly generation: number;
  readonly directory: string;
  readonly proxyURL: string | undefined;
  readonly createdAt: Date;
  readonly context: Promise<BrowserContext>;
  persona: Persona | undefined;
  users: number;
  renders: number;
  lastUsed: number;
};

export type Lease = {
  readonly context: Promise<BrowserContext>;
  readonly proxyURL: string | undefined;
  release(): void;
  discard(): void;
};

export type IdentityView = {
  key: string;
  generation: number;
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

export class Identities {
  readonly #config: Config;
  readonly #log: Log;
  readonly #identities = new Map<string, Identity>();
  readonly #generations = new Map<string, number>();

  constructor(config: Config, log: Log) {
    this.#config = config;
    this.#log = log;
  }

  acquire(key: string, proxyTemplate: string | undefined): Lease {
    let identity = this.#identities.get(key);
    if (identity !== undefined && identity.proxyURL !== withProxySession(proxyTemplate, `${key}:${identity.generation}`)) {
      throw new ServiceError(409, "identity_conflict", `identity ${JSON.stringify(key)} is bound to a different proxy; rotate it first`);
    }
    if (identity === undefined) {
      this.#evictIdle();
      identity = this.#create(key, proxyTemplate);
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
      generation: identity.generation,
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

  async rotate(key: string): Promise<void> {
    const identity = this.#identities.get(key);
    this.#generations.set(key, (this.#generations.get(key) ?? 0) + 1);
    if (identity === undefined) return;
    if (identity.users > 0) throw new ServiceError(409, "identity_conflict", `identity ${JSON.stringify(key)} has renders in progress`);
    this.#identities.delete(key);
    await (await identity.context.catch(() => undefined))?.close();
    await rm(identity.directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
    this.#log("identity_rotated", { key, generation: identity.generation + 1 });
  }

  async close(): Promise<void> {
    const contexts = [...this.#identities.values()].map((identity) => identity.context);
    this.#identities.clear();
    await Promise.all(contexts.map(async (context) => (await context.catch(() => undefined))?.close()));
  }

  #create(key: string, proxyTemplate: string | undefined): Identity {
    const generation = this.#generations.get(key) ?? 0;
    const proxyURL = withProxySession(proxyTemplate, `${key}:${generation}`);
    const directory = join(this.#config.dataDirectory, `identity-${createHash("sha256").update(key).digest("hex").slice(0, 16)}-${generation}`);
    const identity: Identity = {
      key,
      generation,
      directory,
      proxyURL,
      createdAt: new Date(),
      context: this.#launch(key, directory, proxyURL).then(({ context, persona }) => {
        identity.persona = persona;
        return context;
      }),
      persona: undefined,
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
