import { z } from "zod";
import { cookieSchema, httpUrlSchema, identityKeySchema, optionalField, profileNameSchema, proxyUrlSchema, type Cookie } from "../shared/schemas.ts";
import type { Replay } from "./replay.ts";

const maxCaptureRequests = 200;
const maxCaptureBodyBytes = 256 << 10;
const maxCaptureTotalBytes = 2 << 20;

const captureSchema = z.object({
  wait_ms: z.number().int().min(100).max(10_000),
  max_requests: z.number().int().min(1).max(maxCaptureRequests).default(maxCaptureRequests),
  max_body_bytes: z.number().int().min(1).max(maxCaptureBodyBytes).default(maxCaptureBodyBytes),
  max_total_bytes: z.number().int().min(1).max(maxCaptureTotalBytes).default(maxCaptureTotalBytes),
});

const waitForSchema = z.object({
  selector: z.string().min(1).max(500),
  timeout_ms: z.number().int().min(100).max(30_000).default(10_000),
});

export const renderRequestSchema = z.object({
  url: httpUrlSchema,
  timeout_ms: z.number().int().min(1_000).max(120_000).default(45_000),
  wait_until: z.enum(["commit", "domcontentloaded", "load", "networkidle"]).default("domcontentloaded"),
  settle_ms: z.number().int().min(0).max(10_000).default(2_000),
  wait_for: waitForSchema.optional(),
  cookies: z.array(cookieSchema).max(500).default([]),
  proxy_url: optionalField(proxyUrlSchema),
  settle_challenge: z.boolean().default(true),
  solve_captcha: z.boolean().default(true),
  capture: captureSchema.optional(),
  profile: optionalField(profileNameSchema),
  identity: optionalField(identityKeySchema),
});

export type RenderRequest = z.infer<typeof renderRequestSchema>;

export type CaptureLimits = z.infer<typeof captureSchema>;

export type Exchange = {
  id: string;
  url: string;
  method: string;
  resource_type: string;
  frame_id?: string;
  initiator?: string;
  started_ms: number;
  request_headers: Record<string, string>;
  request_body: string;
  status: number;
  mime_type: string;
  response_headers: Record<string, string>;
  response_body: string;
  truncated: boolean;
  error: string;
};

export type Traffic = {
  entries: Exchange[];
  dropped: number;
  duration_ms: number;
};

export type Challenge =
  | { vendor: "datadome"; url?: string }
  | { vendor: "recaptcha"; sitekey?: string; data_s?: string }
  | { vendor: "cloudflare" };

export type RenderResult = {
  status_code: number;
  final_url: string;
  title: string;
  html: string;
  user_agent: string;
  headers: Record<string, string>;
  cookies: ReadonlyArray<Cookie>;
  replay: Replay;
  challenge?: Challenge;
  wait_for_matched?: boolean;
  traffic?: Traffic;
};
