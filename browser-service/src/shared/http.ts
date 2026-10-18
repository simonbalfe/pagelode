import { createHash, timingSafeEqual } from "node:crypto";
import type { MiddlewareHandler } from "hono";
import { ServiceError } from "./errors.ts";

export function requireToken(token: string): MiddlewareHandler {
  const expected = createHash("sha256").update(token).digest();
  return async (c, next) => {
    if (token !== "") {
      const header = c.req.header("authorization");
      const presented = header?.startsWith("Bearer ") ? header.slice("Bearer ".length) : c.req.query("token") ?? "";
      if (!timingSafeEqual(createHash("sha256").update(presented).digest(), expected)) {
        throw new ServiceError(401, "unauthorized", "missing or invalid token");
      }
    }
    await next();
  };
}
