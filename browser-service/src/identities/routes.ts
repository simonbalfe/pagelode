import { Hono } from "hono";
import { httpUrlSchema, identityKeySchema } from "../shared/schemas.ts";
import type { Identities } from "./pool.ts";

export function identityRoutes(identities: Identities): Hono {
  return new Hono()
    .get("/", (c) => c.json({ identities: identities.list() }))
    .get("/:key/clearance", async (c) => {
      const key = identityKeySchema.parse(c.req.param("key"));
      return c.json(await identities.clearance(key, httpUrlSchema.parse(c.req.query("url"))));
    })
    .delete("/:key", async (c) => {
      await identities.rotate(identityKeySchema.parse(c.req.param("key")));
      return c.body(null, 204);
    });
}
