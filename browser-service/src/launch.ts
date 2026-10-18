import type { chromium } from "patchright";
import type { Config } from "./config.ts";

type PersistentContextOptions = NonNullable<Parameters<typeof chromium.launchPersistentContext>[1]>;
type ProxySettings = NonNullable<PersistentContextOptions["proxy"]>;

export type Log = (event: string, values?: Record<string, unknown>) => void;

export function createLog(enabled: boolean): Log {
  return (event, values = {}) => {
    if (!enabled) return;
    process.stderr.write(`${JSON.stringify({ component: "browser-service", event, ...values })}\n`);
  };
}

export type LaunchSettings = {
  headless: boolean;
  proxyURL: string | undefined;
  args?: ReadonlyArray<string>;
  env?: Readonly<Record<string, string>>;
};

export function launchOptions(config: Config, settings: LaunchSettings): PersistentContextOptions {
  const options: PersistentContextOptions = { headless: settings.headless, viewport: null };
  if (config.executablePath !== "") options.executablePath = config.executablePath;
  if (config.channel !== "") options.channel = config.channel;
  const args = [...config.chromeArgs, ...(settings.args ?? [])];
  if (args.length > 0) options.args = args;
  if (settings.env !== undefined && Object.keys(settings.env).length > 0) options.env = { ...process.env, ...settings.env };
  if (settings.proxyURL !== undefined) options.proxy = proxySettings(settings.proxyURL);
  return options;
}

function proxySettings(raw: string): ProxySettings {
  const parsed = new URL(raw);
  const settings: ProxySettings = { server: `${parsed.protocol}//${parsed.host}` };
  if (parsed.username !== "") settings.username = decodeURIComponent(parsed.username);
  if (parsed.password !== "") settings.password = decodeURIComponent(parsed.password);
  return settings;
}
