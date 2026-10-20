import { homedir } from "node:os";
import { join } from "node:path";
import { z } from "zod";

const flag = z.enum(["true", "false"]).transform((value) => value === "true");

const environmentSchema = z.object({
  BROWSER_SERVICE_HOST: z.string().min(1).default("127.0.0.1"),
  BROWSER_SERVICE_PORT: z.coerce.number().int().min(1).max(65_535).default(8090),
  BROWSER_SERVICE_TOKEN: z.string().default(""),
  BROWSER_SERVICE_CONCURRENCY: z.coerce.number().int().min(1).default(16),
  BROWSER_SERVICE_MAX_WAITING: z.coerce.number().int().min(0).default(100),
  BROWSER_SERVICE_MAX_SESSIONS: z.coerce.number().int().min(0).default(4),
  BROWSER_SERVICE_HEADLESS: flag.default(false),
  BROWSER_SERVICE_CHANNEL: z.string().optional(),
  BROWSER_SERVICE_EXECUTABLE_PATH: z.string().default(""),
  BROWSER_SERVICE_CHROME_ARGS: z.string().default(""),
  BROWSER_SERVICE_MAX_IDENTITIES: z.coerce.number().int().min(1).default(8),
  BROWSER_SERVICE_PROFILES_DIR: z.string().default(""),
  BROWSER_SERVICE_DATA_DIR: z.string().default(""),
  BROWSER_SERVICE_DEBUG: flag.default(false),
});

export type Config = {
  readonly host: string;
  readonly port: number;
  readonly token: string;
  readonly concurrency: number;
  readonly maxWaiting: number;
  readonly maxSessions: number;
  readonly headless: boolean;
  readonly channel: string;
  readonly executablePath: string;
  readonly chromeArgs: ReadonlyArray<string>;
  readonly maxIdentities: number;
  readonly profilesDirectory: string;
  readonly dataDirectory: string;
  readonly debug: boolean;
};

function userConfigDirectory(): string {
  if (process.platform === "darwin") return join(homedir(), "Library", "Application Support");
  if (process.platform === "win32") return process.env.APPDATA ?? join(homedir(), "AppData", "Roaming");
  return process.env.XDG_CONFIG_HOME || join(homedir(), ".config");
}

function defaultDataDirectory(): string {
  if (process.platform === "darwin") return join(homedir(), "Library", "Application Support", "PageLode", "Patchright");
  if (process.platform === "win32") return join(process.env.LOCALAPPDATA ?? homedir(), "PageLode", "Patchright");
  return join(homedir(), ".local", "share", "pagelode", "patchright");
}

export function loadConfig(environment: NodeJS.ProcessEnv): Config {
  const present = Object.fromEntries(Object.entries(environment).filter(([name, value]) => value !== "" || name === "BROWSER_SERVICE_CHANNEL"));
  const parsed = environmentSchema.parse(present);
  const host = parsed.BROWSER_SERVICE_HOST;
  if (parsed.BROWSER_SERVICE_TOKEN === "" && !["127.0.0.1", "::1", "localhost"].includes(host)) {
    throw new Error("BROWSER_SERVICE_TOKEN is required when BROWSER_SERVICE_HOST is not a loopback address");
  }
  return {
    host,
    port: parsed.BROWSER_SERVICE_PORT,
    token: parsed.BROWSER_SERVICE_TOKEN,
    concurrency: parsed.BROWSER_SERVICE_CONCURRENCY,
    maxWaiting: parsed.BROWSER_SERVICE_MAX_WAITING,
    maxSessions: parsed.BROWSER_SERVICE_MAX_SESSIONS,
    headless: parsed.BROWSER_SERVICE_HEADLESS,
    channel: parsed.BROWSER_SERVICE_CHANNEL ?? (process.platform === "linux" ? "" : "chrome"),
    executablePath: parsed.BROWSER_SERVICE_EXECUTABLE_PATH,
    chromeArgs: parsed.BROWSER_SERVICE_CHROME_ARGS.split(/\s+/).filter((arg) => arg !== ""),
    maxIdentities: parsed.BROWSER_SERVICE_MAX_IDENTITIES,
    profilesDirectory: parsed.BROWSER_SERVICE_PROFILES_DIR || join(userConfigDirectory(), "pagelode", "profiles"),
    dataDirectory: parsed.BROWSER_SERVICE_DATA_DIR || defaultDataDirectory(),
    debug: parsed.BROWSER_SERVICE_DEBUG,
  };
}
