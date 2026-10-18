import { serveStatic } from "@hono/bun";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { identityRoutes } from "./identities/routes.ts";
import type { Identities } from "./identities/pool.ts";
import type { Renderer } from "./render/renderer.ts";
import { renderRoutes } from "./render/routes.ts";
import { sessionRoutes } from "./sessions/routes.ts";
import type { Sessions } from "./sessions/sessions.ts";
import { ServiceError, toServiceError } from "./shared/errors.ts";
import { requireToken } from "./shared/http.ts";
import type { Limiter } from "./shared/limiter.ts";
import type { Log } from "./shared/log.ts";

const maxBodyBytes = 1 << 20;

export type Services = {
  readonly token: string;
  readonly publicDirectory: string;
  readonly renderer: Renderer;
  readonly sessions: Sessions;
  readonly identities: Identities;
  readonly limiter: Limiter;
  readonly state: { shuttingDown: boolean };
  readonly log: Log;
};

export function createApp(services: Services): Hono {
  const { renderer, sessions, identities, limiter, state, log } = services;
  const api = new Hono()
    .use(requireToken(services.token))
    .use(async (_c, next) => {
      if (state.shuttingDown) throw new ServiceError(503, "shutting_down", "service is stopping");
      await next();
    })
    .use(bodyLimit({
      maxSize: maxBodyBytes,
      onError: () => {
        throw new ServiceError(400, "invalid_request", "request body is too large");
      },
    }))
    .route("/render", renderRoutes(renderer))
    .route("/sessions", sessionRoutes(sessions, log))
    .route("/identities", identityRoutes(identities));

  return new Hono()
    .onError((error, c) => {
      const failure = toServiceError(error);
      if (failure.status >= 500) log("request_failed", { code: failure.code, message: failure.message });
      return c.json({ error: { code: failure.code, message: failure.message } }, failure.status);
    })
    .notFound((c) => c.json({ error: { code: "not_found", message: "route not found" } }, 404))
    .get("/healthz", (c) => c.json({ ok: true, active: limiter.active, waiting: limiter.waiting, sessions: sessions.count }))
    .route("/v1", api)
    .use("/*", serveStatic({ root: services.publicDirectory }));
}
