import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { chromium, type BrowserContext } from "patchright";
import { z } from "zod";
import type { Config } from "./config.ts";
import { ServiceError, type SessionRequest } from "./contract.ts";
import { launchOptions, type Log } from "./launch.ts";
import { personaLaunch, resolvePersona, withProxySession, writePreferences } from "./persona.ts";
import { profileDirectory, restoreSession, saveSession, type ProfileLocks } from "./profiles.ts";

const versionSchema = z.object({ webSocketDebuggerUrl: z.string() });

export type Session = {
  readonly id: string;
  readonly profile: string | undefined;
  readonly headless: boolean;
  readonly expiresAt: Date;
  readonly port: number;
  readonly browserPath: string;
  readonly directory: string;
  readonly context: BrowserContext;
  readonly timer: ReturnType<typeof setTimeout>;
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

  get(id: string): Session | undefined {
    return this.#sessions.get(id);
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
    const session = this.#sessions.get(id);
    if (session === undefined) throw new ServiceError(404, "not_found", "session not found");
    session.closing ??= this.#shutdown(session);
    await session.closing;
  }

  async closeAll(): Promise<void> {
    await Promise.all(this.list().map((session) => this.close(session.id).catch(() => this.#log("session_close_failed", { id: session.id }))));
  }

  async #open(request: SessionRequest): Promise<Session> {
    const profile = request.profile;
    if (profile !== undefined) this.#locks.holdForSession(profile);
    let directory: string | undefined;
    let context: BrowserContext | undefined;
    try {
      directory = profile === undefined
        ? await mkdtemp(join(tmpdir(), "browser-session-"))
        : await profileDirectory(this.#config.profilesDirectory, profile, request.create_profile);
      const id = randomBytes(16).toString("hex");
      const port = await freePort();
      const headless = request.headless ?? this.#config.headless;
      const proxyURL = withProxySession(request.proxy_url, profile === undefined ? `session:${id}` : `profile:${profile}`);
      const persona = await resolvePersona(proxyURL, this.#log);
      await writePreferences(directory, persona, proxyURL !== undefined);
      const launch = personaLaunch(persona);
      context = await chromium.launchPersistentContext(directory, launchOptions(this.#config, {
        headless,
        proxyURL,
        args: [`--remote-debugging-port=${port}`, ...launch.args],
        env: launch.env,
      }));
      if (profile !== undefined) await restoreSession(context, directory);
      const browserPath = await devtoolsBrowserPath(port);
      if (request.start_url !== undefined) {
        const page = context.pages()[0] ?? await context.newPage();
        await page.goto(request.start_url, { waitUntil: "domcontentloaded", timeout: 45_000 });
      }
      const session: Session = {
        id,
        profile,
        headless,
        expiresAt: new Date(Date.now() + request.ttl_seconds * 1_000),
        port,
        browserPath,
        directory,
        context,
        timer: setTimeout(() => void this.close(id).catch(() => this.#log("session_expire_failed", { id })), request.ttl_seconds * 1_000),
      };
      this.#sessions.set(id, session);
      context.once("close", () => void this.#release(session));
      this.#log("session_open", { id, profile, headless, port });
      return session;
    } catch (error: unknown) {
      await context?.close().catch(() => this.#log("session_launch_cleanup_failed"));
      if (profile !== undefined) this.#locks.releaseSession(profile);
      else if (directory !== undefined) await rm(directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
      if (error instanceof ServiceError) throw error;
      throw new ServiceError(502, "render_failed", error instanceof Error ? error.message : "browser session failed to start");
    }
  }

  async #shutdown(session: Session): Promise<void> {
    let saveError: unknown;
    if (session.profile !== undefined) {
      await saveSession(session.context, session.directory).catch((error: unknown) => {
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

async function freePort(): Promise<number> {
  const server = createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  server.close();
  await once(server, "close");
  if (address === null || typeof address === "string") throw new Error("could not allocate a debugging port");
  return address.port;
}

async function devtoolsBrowserPath(port: number): Promise<string> {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    const response = await fetch(`http://127.0.0.1:${port}/json/version`).catch(() => undefined);
    if (response?.ok) return new URL(versionSchema.parse(await response.json()).webSocketDebuggerUrl).pathname;
    await sleep(200);
  }
  throw new Error("browser debugging endpoint did not start");
}
