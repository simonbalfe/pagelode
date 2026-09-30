import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { BrowserContext } from "patchright";
import { z } from "zod";

const sessionSchema = z.object({
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

export async function restoreSession(context: BrowserContext, directory: string): Promise<void> {
  let data: string;
  try {
    data = await readFile(join(directory, "pagelode-session.json"), "utf8");
  } catch (error: unknown) {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") return;
    throw new Error("could not read saved profile session");
  }
  const raw: unknown = JSON.parse(data);
  const result = sessionSchema.safeParse(raw);
  if (!result.success) throw new Error("saved profile session is invalid");
  await context.addCookies(result.data.cookies);
}

export async function saveSession(context: BrowserContext, directory: string): Promise<void> {
  const session = { cookies: await context.cookies() };
  await writeFile(join(directory, "pagelode-session.json"), JSON.stringify(session), { mode: 0o600 });
}
