import { Hono } from "hono";
import type { Renderer } from "./renderer.ts";
import { renderRequestSchema } from "./schema.ts";

export function renderRoutes(renderer: Renderer): Hono {
  return new Hono().post("/", async (c) => {
    const input = renderRequestSchema.parse(await c.req.json());
    return c.json(await renderer.render(input, c.req.raw.signal));
  });
}
