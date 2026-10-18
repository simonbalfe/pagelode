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
  solution: z.object({ cookie: z.string().optional(), gRecaptchaResponse: z.string().optional() }).optional(),
});

type CapSolverResponse = z.infer<typeof responseSchema>;
type Solution = NonNullable<CapSolverResponse["solution"]>;

export type DataDomeTask = {
  readonly captchaUrl: string;
  readonly userAgent: string;
  readonly proxyURL: string;
};

export type RecaptchaV2Task = {
  readonly websiteURL: string;
  readonly websiteKey: string;
  readonly dataS: string | undefined;
  readonly proxyURL: string | undefined;
};

export async function solveDataDome(settings: CapSolverSettings, task: DataDomeTask, signal: AbortSignal): Promise<string> {
  const solution = await runTask(settings, {
    type: "DatadomeSliderTask",
    captchaUrl: task.captchaUrl,
    userAgent: task.userAgent,
    proxy: capsolverProxy(task.proxyURL),
  }, signal);
  if (solution.cookie === undefined) throw new Error("capsolver returned no DataDome cookie");
  return solution.cookie;
}

export async function solveRecaptchaV2(settings: CapSolverSettings, task: RecaptchaV2Task, signal: AbortSignal): Promise<string> {
  const solution = await runTask(settings, {
    type: task.proxyURL === undefined ? "ReCaptchaV2TaskProxyLess" : "ReCaptchaV2Task",
    websiteURL: task.websiteURL,
    websiteKey: task.websiteKey,
    ...(task.dataS === undefined ? {} : { recaptchaDataSValue: task.dataS }),
    ...(task.proxyURL === undefined ? {} : { proxy: capsolverProxy(task.proxyURL) }),
  }, signal);
  if (solution.gRecaptchaResponse === undefined) throw new Error("capsolver returned no reCAPTCHA token");
  return solution.gRecaptchaResponse;
}

async function runTask(settings: CapSolverSettings, task: Record<string, unknown>, signal: AbortSignal): Promise<Solution> {
  const created = await call(settings, "/createTask", { task }, signal);
  if (created.taskId === undefined) throw new Error("capsolver did not return a task id");
  for (let poll = 0; poll < maxPolls; poll += 1) {
    await sleep(pollIntervalMs, undefined, { signal });
    const result = await call(settings, "/getTaskResult", { taskId: created.taskId }, signal);
    if (result.status === "ready") return result.solution ?? {};
    if (result.status === "failed") throw new Error(`capsolver failed to solve ${String(task.type)}`);
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
