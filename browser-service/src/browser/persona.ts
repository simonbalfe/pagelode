import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { z } from "zod";
import type { Log } from "../shared/log.ts";

export type Persona = {
  readonly ip?: string;
  readonly country?: string;
  readonly timezone?: string;
  readonly languages: ReadonlyArray<string>;
};

const countryLanguages: Readonly<Record<string, ReadonlyArray<string>>> = {
  US: ["en-US", "en"],
  GB: ["en-GB", "en-US", "en"],
  IE: ["en-IE", "en-GB", "en-US", "en"],
  CA: ["en-CA", "en-US", "en", "fr-CA"],
  AU: ["en-AU", "en-GB", "en-US", "en"],
  NZ: ["en-NZ", "en-GB", "en-US", "en"],
  ZA: ["en-ZA", "en-GB", "en-US", "en"],
  IN: ["en-IN", "en-GB", "en-US", "en", "hi"],
  SG: ["en-SG", "en-US", "en"],
  DE: ["de-DE", "de", "en-US", "en"],
  AT: ["de-AT", "de", "en-US", "en"],
  CH: ["de-CH", "de", "en-US", "en", "fr-CH"],
  FR: ["fr-FR", "fr", "en-US", "en"],
  BE: ["nl-BE", "nl", "fr-BE", "fr", "en-US", "en"],
  NL: ["nl-NL", "nl", "en-US", "en"],
  ES: ["es-ES", "es", "en-US", "en"],
  PT: ["pt-PT", "pt", "en-US", "en"],
  IT: ["it-IT", "it", "en-US", "en"],
  PL: ["pl-PL", "pl", "en-US", "en"],
  CZ: ["cs-CZ", "cs", "en-US", "en"],
  SE: ["sv-SE", "sv", "en-US", "en"],
  NO: ["nb-NO", "nb", "no", "en-US", "en"],
  DK: ["da-DK", "da", "en-US", "en"],
  FI: ["fi-FI", "fi", "en-US", "en"],
  GR: ["el-GR", "el", "en-US", "en"],
  RO: ["ro-RO", "ro", "en-US", "en"],
  HU: ["hu-HU", "hu", "en-US", "en"],
  TR: ["tr-TR", "tr", "en-US", "en"],
  UA: ["uk-UA", "uk", "ru", "en-US", "en"],
  RU: ["ru-RU", "ru", "en-US", "en"],
  IL: ["he-IL", "he", "en-US", "en"],
  AE: ["en-AE", "ar-AE", "ar", "en-US", "en"],
  SA: ["ar-SA", "ar", "en-US", "en"],
  BR: ["pt-BR", "pt", "en-US", "en"],
  MX: ["es-MX", "es", "en-US", "en"],
  AR: ["es-AR", "es", "en-US", "en"],
  CO: ["es-CO", "es", "en-US", "en"],
  CL: ["es-CL", "es", "en-US", "en"],
  JP: ["ja-JP", "ja", "en-US", "en"],
  KR: ["ko-KR", "ko", "en-US", "en"],
  CN: ["zh-CN", "zh", "en-US", "en"],
  TW: ["zh-TW", "zh", "en-US", "en"],
  HK: ["zh-HK", "zh", "en-US", "en"],
  TH: ["th-TH", "th", "en-US", "en"],
  VN: ["vi-VN", "vi", "en-US", "en"],
  ID: ["id-ID", "id", "en-US", "en"],
  MY: ["en-MY", "ms", "en-US", "en"],
  PH: ["en-PH", "fil", "en-US", "en"],
};

const geoSchema = z.object({ ip: z.string(), country: z.string().length(2).optional(), timezone: z.string().optional() });

export async function resolvePersona(proxyURL: string | undefined, log: Log): Promise<Persona> {
  if (proxyURL === undefined) return { languages: [] };
  if (!proxyURL.startsWith("http:") && !proxyURL.startsWith("https:")) {
    log("persona_lookup_skipped", { reason: "socks proxy" });
    return { languages: [] };
  }
  try {
    const response = await fetch("https://ipinfo.io/json", { proxy: proxyURL, signal: AbortSignal.timeout(8_000) });
    const geo = geoSchema.parse(await response.json());
    const languages = geo.country === undefined ? [] : countryLanguages[geo.country.toUpperCase()] ?? [];
    return {
      ip: geo.ip,
      languages,
      ...(geo.country === undefined ? {} : { country: geo.country.toUpperCase() }),
      ...(geo.timezone === undefined ? {} : { timezone: geo.timezone }),
    };
  } catch (error: unknown) {
    log("persona_lookup_failed", { message: error instanceof Error ? error.message : "unknown" });
    return { languages: [] };
  }
}

export function personaLaunch(persona: Persona): { env: Record<string, string>; args: string[] } {
  const env: Record<string, string> = {};
  const args: string[] = [];
  if (persona.timezone !== undefined) env.TZ = persona.timezone;
  const primary = persona.languages[0];
  if (primary !== undefined) {
    env.LANG = `${primary.replace("-", "_")}.UTF-8`;
    env.LANGUAGE = persona.languages.map((language) => language.replace("-", "_")).join(":");
    args.push(`--lang=${primary}`);
  }
  return { env, args };
}

const preferencesSchema = z.object({
  intl: z.record(z.string(), z.unknown()).optional(),
  webrtc: z.record(z.string(), z.unknown()).optional(),
}).catchall(z.unknown());

export async function writePreferences(directory: string, persona: Persona, proxied: boolean): Promise<void> {
  if (persona.languages.length === 0 && !proxied) return;
  const file = join(directory, "Default", "Preferences");
  await mkdir(join(directory, "Default"), { recursive: true });
  const existing = preferencesSchema.catch({}).parse(await readFile(file, "utf8").then((data) => JSON.parse(data), () => ({})));
  const languages = persona.languages.join(",");
  await writeFile(file, JSON.stringify({
    ...existing,
    ...(languages === "" ? {} : { intl: { ...existing.intl, accept_languages: languages, selected_languages: languages } }),
    ...(proxied ? { webrtc: { ...existing.webrtc, ip_handling_policy: "disable_non_proxied_udp", multiple_routes_enabled: false, nonproxied_udp_enabled: false } } : {}),
  }));
}

export function withProxySession(proxyURL: string | undefined, seed: string): string | undefined {
  if (proxyURL === undefined) return undefined;
  const session = createHash("sha256").update(seed).digest("hex").slice(0, 12);
  return proxyURL.replaceAll("{session}", session).replaceAll("%7Bsession%7D", session).replaceAll("%7bsession%7d", session);
}
