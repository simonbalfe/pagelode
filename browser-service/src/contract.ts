import type { ContentfulStatusCode } from "hono/utils/http-status";
import { z } from "zod";

function emptyAsUndefined<T extends z.ZodType>(schema: T) {
  return z.preprocess((value) => (value === "" ? undefined : value), schema.optional());
}

function hasProtocol(value: string, protocols: ReadonlyArray<string>): boolean {
  try {
    return protocols.includes(new URL(value).protocol);
  } catch {
    return false;
  }
}

const httpURL = z.string().refine((value) => hasProtocol(value, ["http:", "https:"]), "must be an http or https URL");
const proxyURL = z.string().refine((value) => hasProtocol(value, ["http:", "https:", "socks4:", "socks5:"]), "must be an http, https, socks4, or socks5 URL");
export const profileNameSchema = z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/, "profile name must contain 1-64 letters, numbers, underscores, or hyphens and start with a letter or number");
export const identityKeySchema = z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$/, "identity must contain 1-128 letters, numbers, or _.:- and start with a letter or number");

export const cookieSchema = z.object({
  name: z.string().min(1),
  value: z.string(),
  domain: z.string().optional(),
  path: z.string().optional(),
  expires: z.number().optional(),
  http_only: z.boolean().optional(),
  secure: z.boolean().optional(),
  same_site: z.enum(["Strict", "Lax", "None"]).optional(),
});

export type Cookie = z.infer<typeof cookieSchema>;

export const renderRequestSchema = z.object({
  url: httpURL,
  timeout_ms: z.number().int().min(1_000).max(120_000).default(45_000),
  wait_until: z.enum(["commit", "domcontentloaded", "load", "networkidle"]).default("domcontentloaded"),
  cookies: z.array(cookieSchema).max(500).default([]),
  proxy_url: emptyAsUndefined(proxyURL),
  settle_challenge: z.boolean().default(true),
  capture: z.object({ wait_ms: z.number().int().min(100).max(10_000) }).optional(),
  profile: emptyAsUndefined(profileNameSchema),
  identity: emptyAsUndefined(identityKeySchema),
});

export type RenderRequest = z.infer<typeof renderRequestSchema>;

export const sessionRequestSchema = z.object({
  proxy_url: emptyAsUndefined(proxyURL),
  profile: emptyAsUndefined(profileNameSchema),
  create_profile: z.boolean().default(false),
  headless: z.boolean().optional(),
  ttl_seconds: z.number().int().min(30).max(86_400).default(900),
  start_url: emptyAsUndefined(httpURL),
}).refine((request) => !request.create_profile || request.profile !== undefined, {
  message: "create_profile requires profile",
  path: ["create_profile"],
});

export type SessionRequest = z.infer<typeof sessionRequestSchema>;

export const exchangeSchema = z.object({
  id: z.string(),
  url: z.string(),
  method: z.string(),
  resource_type: z.string(),
  frame_id: z.string().optional(),
  initiator: z.string().optional(),
  started_ms: z.number(),
  request_headers: z.record(z.string(), z.string()),
  request_body: z.string(),
  status: z.number(),
  mime_type: z.string(),
  response_headers: z.record(z.string(), z.string()),
  response_body: z.string(),
  truncated: z.boolean(),
  error: z.string(),
});

export type Exchange = z.infer<typeof exchangeSchema>;

export type Traffic = {
  entries: Exchange[];
  dropped: number;
  duration_ms: number;
};

export type RenderResult = {
  status_code: number;
  final_url: string;
  title: string;
  html: string;
  user_agent: string;
  cookies: ReadonlyArray<Cookie>;
  traffic?: Traffic;
};

export type SessionView = {
  id: string;
  cdp_url: string;
  profile?: string;
  headless: boolean;
  expires_at: string;
};

export type IdentityView = {
  key: string;
  generation: number;
  proxy: string;
  ip?: string;
  country?: string;
  timezone?: string;
  languages: ReadonlyArray<string>;
  renders: number;
  active: number;
  created_at: string;
  last_used_at: string;
};

export type ErrorCode =
  | "invalid_request"
  | "unauthorized"
  | "not_found"
  | "profile_not_found"
  | "profile_in_use"
  | "identity_conflict"
  | "saturated"
  | "render_failed"
  | "shutting_down"
  | "internal";

export class ServiceError extends Error {
  readonly status: ContentfulStatusCode;
  readonly code: ErrorCode;

  constructor(status: ContentfulStatusCode, code: ErrorCode, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}
