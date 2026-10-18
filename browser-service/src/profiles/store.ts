import { chmod, lstat, mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import type { BrowserContext } from "patchright";
import { z } from "zod";
import { ServiceError } from "../shared/errors.ts";

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

export async function restoreCookies(context: BrowserContext, directory: string): Promise<void> {
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

export async function saveCookies(context: BrowserContext, directory: string): Promise<void> {
  await writeFile(join(directory, sessionFile), JSON.stringify({ cookies: await context.cookies() }), { mode: 0o600 });
}
