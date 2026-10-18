import { upgradeWebSocket } from "@hono/bun";
import { Hono, type Context } from "hono";
import type { Log } from "../shared/log.ts";
import { relayDevtools } from "./devtools.ts";
import { sessionRequestSchema, type SessionView } from "./schema.ts";
import type { Session, Sessions } from "./sessions.ts";

const sessionId = ":id{[a-f0-9]{32}}";

export function sessionRoutes(sessions: Sessions, log: Log): Hono {
  return new Hono()
    .post("/", async (c) => {
      const session = await sessions.create(sessionRequestSchema.parse(await c.req.json()));
      return c.json(view(session, c), 201);
    })
    .get("/", (c) => c.json({ sessions: sessions.list().map((session) => view(session, c)) }))
    .get(`/${sessionId}`, (c) => c.json(view(sessions.get(c.req.param("id")), c)))
    .delete(`/${sessionId}`, async (c) => {
      await sessions.close(c.req.param("id"));
      return c.body(null, 204);
    })
    .get(`/${sessionId}/cdp`, async (c, next) => {
      const session = sessions.get(c.req.param("id"));
      return upgradeWebSocket(() => relayDevtools(session.port, session.browserPath, log))(c, next);
    })
    .get(`/${sessionId}/devtools/page/:target{[A-Za-z0-9-]+}`, async (c, next) => {
      const session = sessions.get(c.req.param("id"));
      return upgradeWebSocket(() => relayDevtools(session.port, `/devtools/page/${c.req.param("target")}`, log))(c, next);
    });
}

function view(session: Session, c: Context): SessionView {
  const url = new URL(c.req.url);
  const secure = c.req.header("x-forwarded-proto") === "https" || url.protocol === "https:";
  return {
    id: session.id,
    cdp_url: `${secure ? "wss" : "ws"}://${c.req.header("host") ?? url.host}/v1/sessions/${session.id}/cdp`,
    ...(session.profile === undefined ? {} : { profile: session.profile }),
    headless: session.headless,
    expires_at: session.expiresAt.toISOString(),
  };
}
