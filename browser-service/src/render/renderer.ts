import { createHash } from "node:crypto";
import { launchBrowser } from "../browser/launch.ts";
import { withProxySession } from "../browser/persona.ts";
import type { Config } from "../config.ts";
import type { Identities } from "../identities/pool.ts";
import type { ProfileLocks } from "../profiles/locks.ts";
import { profileDirectory, restoreCookies, saveCookies } from "../profiles/store.ts";
import { ServiceError } from "../shared/errors.ts";
import type { Limiter } from "../shared/limiter.ts";
import type { Log } from "../shared/log.ts";
import { renderPage } from "./page.ts";
import type { RenderRequest, RenderResult } from "./schema.ts";

const deadlineGraceMs = 15_000;

export class Renderer {
  readonly #config: Config;
  readonly #limiter: Limiter;
  readonly #locks: ProfileLocks;
  readonly #identities: Identities;
  readonly #log: Log;

  constructor(config: Config, limiter: Limiter, locks: ProfileLocks, identities: Identities, log: Log) {
    this.#config = config;
    this.#limiter = limiter;
    this.#locks = locks;
    this.#identities = identities;
    this.#log = log;
  }

  async render(input: RenderRequest, signal: AbortSignal): Promise<RenderResult> {
    try {
      if (input.profile !== undefined) return await this.#renderInProfile(input, input.profile, signal);
      return await this.#limiter.run(signal, () => this.#renderWithIdentity(input, signal));
    } catch (error: unknown) {
      if (error instanceof ServiceError) throw error;
      if (signal.aborted) throw signal.reason;
      throw new ServiceError(502, "render_failed", error instanceof Error ? error.message : "browser render failed");
    }
  }

  async #renderWithIdentity(input: RenderRequest, signal: AbortSignal): Promise<RenderResult> {
    const key = input.identity ?? (input.proxy_url === undefined ? "default" : `proxy-${createHash("sha256").update(input.proxy_url).digest("hex").slice(0, 12)}`);
    const lease = this.#identities.acquire(key, input.proxy_url);
    const limitMs = input.timeout_ms + deadlineGraceMs;
    const deadline = Promise.withResolvers<never>();
    const timer = setTimeout(() => {
      lease.discard();
      deadline.reject(new ServiceError(504, "render_failed", `render exceeded ${limitMs} ms; browser restarted`));
    }, limitMs);
    try {
      const result = await Promise.race([lease.context.then((context) => renderPage(context, input, { signal, log: this.#log })), deadline.promise]);
      lease.recordHeaders(result.replay.headers);
      return { ...result, replay: { ...result.replay, identity: key, ...(lease.proxyURL === undefined ? {} : { proxy_url: lease.proxyURL }) } };
    } finally {
      clearTimeout(timer);
      lease.release();
    }
  }

  async #renderInProfile(input: RenderRequest, name: string, signal: AbortSignal): Promise<RenderResult> {
    const directory = await profileDirectory(this.#config.profilesDirectory, name, false);
    return this.#locks.withRender(name, () => this.#limiter.run(signal, async () => {
      const proxyURL = withProxySession(input.proxy_url, `profile:${name}`);
      const { context } = await launchBrowser(this.#config, this.#log, { directory, proxyURL, headless: this.#config.headless });
      try {
        await restoreCookies(context, directory);
        const result = await renderPage(context, input, { signal, log: this.#log });
        await saveCookies(context, directory);
        return { ...result, replay: { ...result.replay, ...(proxyURL === undefined ? {} : { proxy_url: proxyURL }) } };
      } finally {
        await context.close();
      }
    }));
  }
}
