import { Hono } from "hono";
import { identityKeySchema } from "../shared/schemas.ts";
import type { Identities } from "./pool.ts";

export function identityRoutes(identities: Identities): Hono {
  return new Hono()
    .get("/", (c) => c.json({ identities: identities.list() }))
    .delete("/:key", async (c) => {
      await identities.rotate(identityKeySchema.parse(c.req.param("key")));
      return c.body(null, 204);
    });
}
