import { chromium, type BrowserContext } from "patchright";
import type { Config } from "../config.ts";
import type { Log } from "../shared/log.ts";
import { personaLaunch, resolvePersona, writePreferences, type Persona } from "./persona.ts";

type PersistentContextOptions = NonNullable<Parameters<typeof chromium.launchPersistentContext>[1]>;
type ProxySettings = NonNullable<PersistentContextOptions["proxy"]>;

const launchTimeoutMs = 30_000;

export type LaunchRequest = {
  readonly directory: string;
  readonly proxyURL: string | undefined;
  readonly headless: boolean;
  readonly args?: ReadonlyArray<string>;
};

export type LaunchedBrowser = {
  readonly context: BrowserContext;
  readonly persona: Persona;
};

export async function launchBrowser(config: Config, log: Log, request: LaunchRequest): Promise<LaunchedBrowser> {
  const persona = await resolvePersona(request.proxyURL, log);
  await writePreferences(request.directory, persona, request.proxyURL !== undefined);
  const { env, args } = personaLaunch(persona);
  const options: PersistentContextOptions = {
    headless: request.headless,
    timeout: launchTimeoutMs,
    viewport: null,
    args: [...config.chromeArgs, ...(request.args ?? []), ...args],
  };
  if (config.executablePath !== "") options.executablePath = config.executablePath;
  if (config.channel !== "") options.channel = config.channel;
  if (Object.keys(env).length > 0) options.env = { ...process.env, ...env };
  if (request.proxyURL !== undefined) options.proxy = proxySettings(request.proxyURL);
  const context = await chromium.launchPersistentContext(request.directory, options);
  return { context, persona };
}

function proxySettings(raw: string): ProxySettings {
  const parsed = new URL(raw);
  const settings: ProxySettings = { server: `${parsed.protocol}//${parsed.host}` };
  if (parsed.username !== "") settings.username = decodeURIComponent(parsed.username);
  if (parsed.password !== "") settings.password = decodeURIComponent(parsed.password);
  return settings;
}
