import { websocket } from "@hono/bun";
import { join } from "node:path";
import { createApp } from "./app.ts";
import { loadConfig } from "./config.ts";
import { Identities } from "./identities/pool.ts";
import { ProfileLocks } from "./profiles/locks.ts";
import { Renderer } from "./render/renderer.ts";
import { Sessions } from "./sessions/sessions.ts";
import { Limiter } from "./shared/limiter.ts";
import { createLog } from "./shared/log.ts";

const config = loadConfig(Bun.env);
const log = createLog(config.debug);
const limiter = new Limiter(config.concurrency, config.maxWaiting);
const locks = new ProfileLocks();
const identities = new Identities(config, log);
const sessions = new Sessions(config, locks, log);
const state = { shuttingDown: false };

const app = createApp({
  token: config.token,
  publicDirectory: join(import.meta.dir, "..", "public"),
  renderer: new Renderer(config, limiter, locks, identities, log),
  sessions,
  identities,
  limiter,
  state,
  log,
});

const server = Bun.serve({
  hostname: config.host,
  port: config.port,
  fetch: app.fetch,
  websocket: { ...websocket, idleTimeout: 960 },
  idleTimeout: 255,
});
process.stderr.write(`${JSON.stringify({ component: "browser-service", event: "listening", url: server.url.href })}\n`);

async function stop(): Promise<void> {
  if (state.shuttingDown) return;
  state.shuttingDown = true;
  await server.stop();
  await sessions.closeAll();
  await identities.close();
  process.exit(0);
}

process.once("SIGTERM", () => void stop());
process.once("SIGINT", () => void stop());
