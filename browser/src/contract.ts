import { z } from "zod";

const cookieSchema = z.object({
  name: z.string().min(1),
  value: z.string(),
  domain: z.string().optional(),
  path: z.string().optional(),
  expires: z.number().optional(),
  http_only: z.boolean().optional(),
  secure: z.boolean().optional(),
  same_site: z.enum(["Strict", "Lax", "None"]).optional(),
});

export const renderRequestSchema = z.object({
  id: z.string().min(1),
  url: z.url().refine((value) => {
    const protocol = new URL(value).protocol;
    return protocol === "http:" || protocol === "https:";
  }),
  timeout_ms: z.number().int().min(1_000).max(60_000).default(45_000),
  wait_until: z.enum(["commit", "domcontentloaded", "load", "networkidle"]).default("domcontentloaded"),
  cookies: z.array(cookieSchema).max(500).default([]),
  proxy_url: z.string().optional(),
  settle_challenge: z.boolean().default(true),
  capture: z.object({ wait_ms: z.number().int().min(100).max(10_000) }).optional(),
});

export type RenderRequest = z.infer<typeof renderRequestSchema>;

export type RenderData = {
  status_code: number;
  final_url: string;
  title: string;
  html: string;
  user_agent: string;
  cookies: ReadonlyArray<z.infer<typeof cookieSchema>>;
  traffic?: Traffic;
};

export type RenderEnvelope =
  | { id: string; ok: true; data: RenderData }
  | { id: string; ok: false; error: { code: string; message: string } };

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
