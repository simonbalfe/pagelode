import { z } from "zod";

export function optionalField<T extends z.ZodType>(schema: T) {
  return z.preprocess((value) => (value === "" ? undefined : value), schema.optional());
}

function urlWithProtocol(protocols: ReadonlyArray<string>, message: string) {
  return z.string().refine((value) => URL.canParse(value) && protocols.includes(new URL(value).protocol), message);
}

export const httpUrlSchema = urlWithProtocol(["http:", "https:"], "must be an http or https URL");

export const proxyUrlSchema = urlWithProtocol(["http:", "https:", "socks4:", "socks5:"], "must be an http, https, socks4, or socks5 URL");

export const profileNameSchema = z.string().regex(
  /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/,
  "profile name must contain 1-64 letters, numbers, underscores, or hyphens and start with a letter or number",
);

export const identityKeySchema = z.string().regex(
  /^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$/,
  "identity must contain 1-128 letters, numbers, or _.:- and start with a letter or number",
);

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
