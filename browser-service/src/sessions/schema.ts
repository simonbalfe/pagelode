import { z } from "zod";
import { httpUrlSchema, optionalField, profileNameSchema, proxyUrlSchema } from "../shared/schemas.ts";

export const sessionRequestSchema = z.object({
  proxy_url: optionalField(proxyUrlSchema),
  profile: optionalField(profileNameSchema),
  create_profile: z.boolean().default(false),
  headless: z.boolean().optional(),
  ttl_seconds: z.number().int().min(30).max(86_400).default(900),
  start_url: optionalField(httpUrlSchema),
}).refine((request) => !request.create_profile || request.profile !== undefined, {
  message: "create_profile requires profile",
  path: ["create_profile"],
});

export type SessionRequest = z.infer<typeof sessionRequestSchema>;

export type SessionView = {
  id: string;
  cdp_url: string;
  profile?: string;
  headless: boolean;
  expires_at: string;
};
