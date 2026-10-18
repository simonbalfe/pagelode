import { z } from "zod";
import { cookieSchema, httpUrlSchema, identityKeySchema, optionalField, profileNameSchema, proxyUrlSchema, type Cookie } from "../shared/schemas.ts";

export const renderRequestSchema = z.object({
  url: httpUrlSchema,
  timeout_ms: z.number().int().min(1_000).max(120_000).default(45_000),
  wait_until: z.enum(["commit", "domcontentloaded", "load", "networkidle"]).default("domcontentloaded"),
  cookies: z.array(cookieSchema).max(500).default([]),
  proxy_url: optionalField(proxyUrlSchema),
  settle_challenge: z.boolean().default(true),
  solve_captcha: z.boolean().default(true),
  capture: z.object({ wait_ms: z.number().int().min(100).max(10_000) }).optional(),
  profile: optionalField(profileNameSchema),
  identity: optionalField(identityKeySchema),
});

export type RenderRequest = z.infer<typeof renderRequestSchema>;

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

export type RenderResult = {
  status_code: number;
  final_url: string;
  title: string;
  html: string;
  user_agent: string;
  cookies: ReadonlyArray<Cookie>;
  traffic?: Traffic;
};
