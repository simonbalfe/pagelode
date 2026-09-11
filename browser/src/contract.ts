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
  user_agent: z.string().min(1).optional(),
  cookies: z.array(cookieSchema).max(500).default([]),
  proxy_url: z.string().optional(),
  settle_challenge: z.boolean().default(true),
});

export type RenderRequest = z.infer<typeof renderRequestSchema>;

export type RenderData = {
  status_code: number;
  final_url: string;
  title: string;
  html: string;
  user_agent: string;
  cookies: ReadonlyArray<z.infer<typeof cookieSchema>>;
};

export type RenderEnvelope =
  | { id: string; ok: true; data: RenderData }
  | { id: string; ok: false; error: { code: string; message: string } };
