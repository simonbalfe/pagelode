import { chmod, lstat, mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import type { BrowserContext } from "patchright";
import { z } from "zod";
import { ServiceError } from "./contract.ts";

const sessionFile = "pagelode-session.json";

const savedSessionSchema = z.object({
  cookies: z.array(z.object({
    name: z.string(),
    value: z.string(),
    domain: z.string(),
    path: z.string(),
    expires: z.number(),
    httpOnly: z.boolean(),
    secure: z.boolean(),
    sameSite: z.enum(["Strict", "Lax", "None"]),
  })),
});

export async function profileDirectory(root: string, name: string, create: boolean): Promise<string> {
  const directory = resolve(join(root, name));
  if (create) await mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await lstat(directory).catch((error: unknown) => {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") {
      throw new ServiceError(404, "profile_not_found", `profile ${JSON.stringify(name)} does not exist; create it with a login session`);
    }
    throw error;
  });
  if (!info.isDirectory() || info.isSymbolicLink()) throw new ServiceError(400, "invalid_request", "profile must be a directory, not a symlink");
  await chmod(directory, 0o700);
  return directory;
}

export async function restoreSession(context: BrowserContext, directory: string): Promise<void> {
  let data: string;
  try {
    data = await readFile(join(directory, sessionFile), "utf8");
  } catch (error: unknown) {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") return;
    throw new Error("could not read saved profile session");
  }
  const result = savedSessionSchema.safeParse(JSON.parse(data));
  if (!result.success) throw new Error("saved profile session is invalid");
  await context.addCookies(result.data.cookies);
}

export async function saveSession(context: BrowserContext, directory: string): Promise<void> {
  await writeFile(join(directory, sessionFile), JSON.stringify({ cookies: await context.cookies() }), { mode: 0o600 });
}

export class ProfileLocks {
  readonly #renders = new Map<string, Promise<void>>();
  readonly #sessions = new Set<string>();

  async withRender<T>(name: string, task: () => Promise<T>): Promise<T> {
    if (this.#sessions.has(name)) throw new ServiceError(409, "profile_in_use", `profile ${JSON.stringify(name)} is held by an open session`);
    const previous = this.#renders.get(name) ?? Promise.resolve();
    const { promise: done, resolve: release } = Promise.withResolvers<void>();
    const tail = previous.then(() => done);
    this.#renders.set(name, tail);
    await previous;
    try {
      return await task();
    } finally {
      release();
      if (this.#renders.get(name) === tail) this.#renders.delete(name);
    }
  }

  holdForSession(name: string): void {
    if (this.#sessions.has(name) || this.#renders.has(name)) throw new ServiceError(409, "profile_in_use", `profile ${JSON.stringify(name)} is in use`);
    this.#sessions.add(name);
  }

  releaseSession(name: string): void {
    this.#sessions.delete(name);
  }
}
