import { once } from "node:events";
import { createServer } from "node:net";
import { setTimeout as sleep } from "node:timers/promises";
import type { WSEvents, WSMessageReceive } from "hono/ws";
import { z } from "zod";
import type { Log } from "../shared/log.ts";

const versionSchema = z.object({ webSocketDebuggerUrl: z.string() });

export async function freeDebuggingPort(): Promise<number> {
  const server = createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  server.close();
  await once(server, "close");
  if (address === null || typeof address === "string") throw new Error("could not allocate a debugging port");
  return address.port;
}

export async function browserDevtoolsPath(port: number): Promise<string> {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    const response = await fetch(`http://127.0.0.1:${port}/json/version`).catch(() => undefined);
    if (response?.ok) return new URL(versionSchema.parse(await response.json()).webSocketDebuggerUrl).pathname;
    await sleep(200);
  }
  throw new Error("browser debugging endpoint did not start");
}

export function relayDevtools(port: number, path: string, log: Log): WSEvents {
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
