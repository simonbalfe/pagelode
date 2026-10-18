import { createHash, timingSafeEqual } from "node:crypto";
import { serveStatic, upgradeWebSocket } from "@hono/bun";
import { Hono, type Context } from "hono";
import { bodyLimit } from "hono/body-limit";
import type { WSEvents, WSMessageReceive } from "hono/ws";
import { ZodError } from "zod";
import {
  identityKeySchema,
  renderRequestSchema,
  ServiceError,
  sessionRequestSchema,
  type IdentityView,
  type RenderRequest,
  type RenderResult,
  type SessionRequest,
  type SessionView,
} from "./contract.ts";
import type { Log } from "./launch.ts";

export type SessionRecord = {
  readonly id: string;
  readonly profile: string | undefined;
  readonly headless: boolean;
  readonly expiresAt: Date;
  readonly port: number;
  readonly browserPath: string;
};

export type AppDependencies = {
  readonly token: string;
  readonly publicDirectory: string;
  readonly renderer: { render(input: RenderRequest, signal: AbortSignal): Promise<RenderResult> };
  readonly sessions: {
    readonly count: number;
    create(request: SessionRequest): Promise<SessionRecord>;
    list(): ReadonlyArray<SessionRecord>;
    get(id: string): SessionRecord | undefined;
    close(id: string): Promise<void>;
  };
  readonly identities: {
    list(): ReadonlyArray<IdentityView>;
    rotate(key: string): Promise<void>;
  };
  readonly load: { readonly active: number; readonly waiting: number };
  readonly state: { shuttingDown: boolean };
  readonly log: Log;
};

export function createApp(dependencies: AppDependencies): Hono {
  const { renderer, sessions } = dependencies;
  const app = new Hono();

  app.onError((error, c) => {
    const failure = toServiceError(error);
    if (failure.status >= 500) dependencies.log("request_failed", { code: failure.code, message: failure.message });
    return c.json({ error: { code: failure.code, message: failure.message } }, failure.status);
  });
  app.notFound((c) => c.json({ error: { code: "not_found", message: "route not found" } }, 404));

  app.get("/healthz", (c) => c.json({ ok: true, active: dependencies.load.active, waiting: dependencies.load.waiting, sessions: sessions.count }));

  app.use("/v1/*", async (c, next) => {
    if (!authorized(c.req.header("authorization"), c.req.query("token"), dependencies.token)) throw new ServiceError(401, "unauthorized", "missing or invalid token");
    if (dependencies.state.shuttingDown) throw new ServiceError(503, "shutting_down", "service is stopping");
    await next();
  });
  app.use("/v1/*", bodyLimit({ maxSize: 1 << 20, onError: () => { throw new ServiceError(400, "invalid_request", "request body is too large"); } }));

  app.post("/v1/render", async (c) => c.json(await renderer.render(renderRequestSchema.parse(await body(c)), c.req.raw.signal)));

  app.post("/v1/sessions", async (c) => c.json(view(await sessions.create(sessionRequestSchema.parse(await body(c))), origin(c)), 201));
  app.get("/v1/sessions", (c) => c.json({ sessions: sessions.list().map((session) => view(session, origin(c))) }));
  app.get("/v1/sessions/:id{[a-f0-9]{32}}", (c) => c.json(view(requireSession(sessions, c.req.param("id")), origin(c))));
  app.delete("/v1/sessions/:id{[a-f0-9]{32}}", async (c) => {
    await sessions.close(c.req.param("id"));
    return c.body(null, 204);
  });

  app.get("/v1/identities", (c) => c.json({ identities: dependencies.identities.list() }));
  app.delete("/v1/identities/:key", async (c) => {
    await dependencies.identities.rotate(identityKeySchema.parse(c.req.param("key")));
    return c.body(null, 204);
  });

  app.get("/v1/sessions/:id{[a-f0-9]{32}}/cdp", async (c, next) => {
    const session = requireSession(sessions, c.req.param("id"));
    return upgradeWebSocket(() => relay(session.port, session.browserPath, dependencies.log))(c, next);
  });
  app.get("/v1/sessions/:id{[a-f0-9]{32}}/devtools/page/:target{[A-Za-z0-9-]+}", async (c, next) => {
    const session = requireSession(sessions, c.req.param("id"));
    return upgradeWebSocket(() => relay(session.port, `/devtools/page/${c.req.param("target")}`, dependencies.log))(c, next);
  });

  app.use("/*", serveStatic({ root: dependencies.publicDirectory }));
  return app;
}

function toServiceError(error: unknown): ServiceError {
  if (error instanceof ServiceError) return error;
  if (error instanceof ZodError) {
    return new ServiceError(400, "invalid_request", error.issues.map((issue) => `${issue.path.join(".") || "body"}: ${issue.message}`).join("; "));
  }
  return new ServiceError(500, "internal", error instanceof Error ? error.message : "unexpected failure");
}

function authorized(header: string | undefined, query: string | undefined, token: string): boolean {
  if (token === "") return true;
  const presented = header?.startsWith("Bearer ") ? header.slice("Bearer ".length) : query ?? "";
  return timingSafeEqual(createHash("sha256").update(presented).digest(), createHash("sha256").update(token).digest());
}

async function body(c: Context): Promise<unknown> {
  return c.req.json().catch(() => {
    throw new ServiceError(400, "invalid_request", "request body must be JSON");
  });
}

function origin(c: Context): string {
  const url = new URL(c.req.url);
  const secure = c.req.header("x-forwarded-proto") === "https" || url.protocol === "https:";
  return `${secure ? "wss" : "ws"}://${c.req.header("host") ?? url.host}`;
}

function requireSession(sessions: AppDependencies["sessions"], id: string): SessionRecord {
  const session = sessions.get(id);
  if (session === undefined) throw new ServiceError(404, "not_found", "session not found");
  return session;
}

function view(session: SessionRecord, base: string): SessionView {
  return {
    id: session.id,
    cdp_url: `${base}/v1/sessions/${session.id}/cdp`,
    ...(session.profile === undefined ? {} : { profile: session.profile }),
    headless: session.headless,
    expires_at: session.expiresAt.toISOString(),
  };
}

function relay(port: number, path: string, log: Log): WSEvents {
  let upstream: WebSocket | undefined;
  const pending: WSMessageReceive[] = [];
  return {
    onOpen(_event, client) {
      const socket = new WebSocket(`ws://127.0.0.1:${port}${path}`);
      socket.binaryType = "arraybuffer";
      upstream = socket;
      socket.addEventListener("open", () => {
        for (const message of pending.splice(0)) socket.send(message);
      });
      socket.addEventListener("message", (event: MessageEvent<string | ArrayBuffer>) => client.send(event.data));
      socket.addEventListener("close", () => client.close());
      socket.addEventListener("error", () => {
        log("devtools_upstream_failed", { port, path });
        client.close(1011, "browser connection failed");
      });
    },
    onMessage(event) {
      if (upstream?.readyState === WebSocket.OPEN) upstream.send(event.data);
      else pending.push(event.data);
    },
    onClose() {
      upstream?.close();
    },
  };
}
