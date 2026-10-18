import { setTimeout as sleep } from "node:timers/promises";
import { z } from "zod";
import type { CapSolverSettings } from "../config.ts";

const pollIntervalMs = 1_000;
const maxPolls = 90;

const responseSchema = z.object({
  errorId: z.number(),
  errorCode: z.string().nullish(),
  errorDescription: z.string().nullish(),
  taskId: z.string().optional(),
  status: z.string().optional(),
  solution: z.object({ cookie: z.string().optional() }).optional(),
});

type CapSolverResponse = z.infer<typeof responseSchema>;

export type DataDomeTask = {
  readonly captchaUrl: string;
  readonly userAgent: string;
  readonly proxyURL: string;
};

export async function solveDataDome(settings: CapSolverSettings, task: DataDomeTask, signal: AbortSignal): Promise<string> {
  const created = await call(settings, "/createTask", {
    task: { type: "DatadomeSliderTask", captchaUrl: task.captchaUrl, userAgent: task.userAgent, proxy: capsolverProxy(task.proxyURL) },
  }, signal);
  if (created.taskId === undefined) throw new Error("capsolver did not return a task id");
  for (let poll = 0; poll < maxPolls; poll += 1) {
    await sleep(pollIntervalMs, undefined, { signal });
    const result = await call(settings, "/getTaskResult", { taskId: created.taskId }, signal);
    if (result.status === "ready") {
      if (result.solution?.cookie === undefined) throw new Error("capsolver returned no DataDome cookie");
      return result.solution.cookie;
    }
    if (result.status === "failed") throw new Error("capsolver failed to solve the DataDome challenge");
  }
  throw new Error("capsolver did not finish in time");
}

async function call(settings: CapSolverSettings, path: string, body: Record<string, unknown>, signal: AbortSignal): Promise<CapSolverResponse> {
  const response = await fetch(new URL(path, settings.url), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ clientKey: settings.apiKey, ...body }),
    signal,
  });
  const parsed = responseSchema.parse(await response.json());
  if (parsed.errorId !== 0) throw new Error(`capsolver ${parsed.errorCode ?? "error"}: ${parsed.errorDescription ?? "request failed"}`);
  return parsed;
}

function capsolverProxy(raw: string): string {
  const url = new URL(raw);
  const credentials = url.username === "" ? "" : `:${decodeURIComponent(url.username)}:${decodeURIComponent(url.password)}`;
  return `${url.hostname}:${url.port}${credentials}`;
}
