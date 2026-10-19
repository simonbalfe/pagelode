import { randomBytes } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { BrowserContext } from "patchright";
import { launchBrowser } from "../browser/launch.ts";
import type { Config } from "../config.ts";
import type { ProfileLocks } from "../profiles/locks.ts";
import { profileDirectory, restoreCookies, saveCookies } from "../profiles/store.ts";
import { ServiceError } from "../shared/errors.ts";
import type { Log } from "../shared/log.ts";
import { browserDevtoolsPath, freeDebuggingPort } from "./devtools.ts";
import type { SessionRequest } from "./schema.ts";

export type Session = {
  readonly id: string;
  readonly profile: string | undefined;
  readonly headless: boolean;
  readonly expiresAt: Date;
  readonly port: number;
  readonly browserPath: string;
  readonly directory: string;
  readonly context: BrowserContext;
  readonly timer: Timer;
  closing?: Promise<void>;
};

export class Sessions {
  readonly #config: Config;
  readonly #locks: ProfileLocks;
  readonly #log: Log;
  readonly #sessions = new Map<string, Session>();
  #starting = 0;

  constructor(config: Config, locks: ProfileLocks, log: Log) {
    this.#config = config;
    this.#locks = locks;
    this.#log = log;
  }

  get count(): number {
    return this.#sessions.size + this.#starting;
  }

  list(): ReadonlyArray<Session> {
    return [...this.#sessions.values()];
  }

  get(id: string): Session {
    const session = this.#sessions.get(id);
    if (session === undefined) throw new ServiceError(404, "not_found", "session not found");
    return session;
  }

  async create(request: SessionRequest): Promise<Session> {
    if (this.count >= this.#config.maxSessions) throw new ServiceError(429, "saturated", "session limit reached");
    this.#starting += 1;
    try {
      return await this.#open(request);
    } finally {
      this.#starting -= 1;
    }
  }

  async close(id: string): Promise<void> {
    const session = this.get(id);
    session.closing ??= this.#shutdown(session);
    await session.closing;
  }

  async closeAll(): Promise<void> {
    await Promise.all(this.list().map((session) => this.close(session.id).catch(() => this.#log("session_close_failed", { id: session.id }))));
  }

  async #open(request: SessionRequest): Promise<Session> {
    const { profile } = request;
    if (profile !== undefined) this.#locks.holdForSession(profile);
    let directory: string | undefined;
    let context: BrowserContext | undefined;
    try {
      directory = profile === undefined
        ? await mkdtemp(join(tmpdir(), "browser-session-"))
        : await profileDirectory(this.#config.profilesDirectory, profile, request.create_profile);
      const id = randomBytes(16).toString("hex");
      const port = await freeDebuggingPort();
      const headless = request.headless ?? this.#config.headless;
      ({ context } = await launchBrowser(this.#config, this.#log, { directory, proxyURL: request.proxy_url, headless, args: [`--remote-debugging-port=${port}`] }));
      if (profile !== undefined) await restoreCookies(context, directory);
      const browserPath = await browserDevtoolsPath(port);
      if (request.start_url !== undefined) await (context.pages()[0] ?? await context.newPage()).goto(request.start_url, { waitUntil: "domcontentloaded", timeout: 45_000 });
      return this.#register({ id, profile, headless, port, browserPath, directory, context }, request.ttl_seconds);
    } catch (error: unknown) {
      await context?.close().catch(() => this.#log("session_launch_cleanup_failed"));
      if (profile !== undefined) this.#locks.releaseSession(profile);
      else if (directory !== undefined) await rm(directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
      if (error instanceof ServiceError) throw error;
      throw new ServiceError(502, "render_failed", error instanceof Error ? error.message : "browser session failed to start");
    }
  }

  #register(opened: Omit<Session, "expiresAt" | "timer" | "closing">, ttlSeconds: number): Session {
    const session: Session = {
      ...opened,
      expiresAt: new Date(Date.now() + ttlSeconds * 1_000),
      timer: setTimeout(() => void this.close(opened.id).catch(() => this.#log("session_expire_failed", { id: opened.id })), ttlSeconds * 1_000),
    };
    this.#sessions.set(session.id, session);
    session.context.once("close", () => void this.#release(session));
    this.#log("session_open", { id: session.id, profile: session.profile, headless: session.headless, port: session.port });
    return session;
  }

  async #shutdown(session: Session): Promise<void> {
    let saveError: unknown;
    if (session.profile !== undefined) {
      await saveCookies(session.context, session.directory).catch((error: unknown) => {
        saveError = error;
      });
    }
    await this.#release(session);
    if (saveError !== undefined) {
      throw new ServiceError(500, "internal", `could not save profile: ${saveError instanceof Error ? saveError.message : "unknown error"}`);
    }
  }

  async #release(session: Session): Promise<void> {
    if (this.#sessions.get(session.id) !== session) return;
    this.#sessions.delete(session.id);
    clearTimeout(session.timer);
    await session.context.close().catch(() => this.#log("session_browser_close_failed", { id: session.id }));
    if (session.profile !== undefined) this.#locks.releaseSession(session.profile);
    else await rm(session.directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
    this.#log("session_closed", { id: session.id });
  }
}
