import type { Page, Request, Response } from "patchright";
import type { Exchange, Traffic } from "./contract";

const maxRequests = 200;
const maxBodyBytes = 256 << 10;
const maxTotalBytes = 2 << 20;

export function captureTraffic(page: Page): { finish: () => Promise<Traffic> } {
  const started = Date.now();
  const entries: Exchange[] = [];
  const requests = new Map<Request, Exchange>();
  const pending: Promise<void>[] = [];
  let remaining = maxTotalBytes;
  let dropped = 0;

  function requestStarted(request: Request): void {
    if (entries.length >= maxRequests) { dropped += 1; return; }
    const body = request.postData() ?? "";
    const bytes = Buffer.byteLength(body);
    const truncated = bytes > maxBodyBytes || bytes > remaining;
    if (!truncated) remaining -= bytes;
    const entry: Exchange = {
      id: `r${entries.length + 1}`,
      url: request.url(),
      method: request.method(),
      resource_type: request.resourceType(),
      started_ms: Date.now() - started,
      request_headers: request.headers(),
      request_body: truncated ? "" : body,
      status: 0,
      mime_type: "",
      response_headers: {},
      response_body: "",
      truncated,
      error: "request incomplete at end of observation",
    };
    entries.push(entry);
    requests.set(request, entry);
  }

  function responseReceived(response: Response): void {
    const entry = requests.get(response.request());
    if (entry === undefined) return;
    entry.status = response.status();
    entry.response_headers = response.headers();
    entry.mime_type = response.headers()["content-type"] ?? "";
  }

  async function readResponse(request: Request): Promise<void> {
    const entry = requests.get(request);
    if (entry === undefined) return;
    entry.error = "";
    const response = await request.response();
    if (response === null) { entry.error = "response unavailable"; return; }
    entry.request_headers = await request.allHeaders();
    const mime = entry.mime_type.toLowerCase();
    const useful = ["fetch", "xhr", "document"].includes(entry.resource_type) || mime.includes("json");
    if (!useful || mime.includes("event-stream") || entry.status === 204 || (entry.status >= 300 && entry.status < 400)) return;
    const length = Number(entry.response_headers["content-length"] ?? "0");
    if (length > maxBodyBytes || remaining === 0) { entry.truncated = true; return; }
    const body = await response.body();
    if (body.length > maxBodyBytes || body.length > remaining) { entry.truncated = true; return; }
    remaining -= body.length;
    entry.response_body = body.toString("utf8");
  }

  function requestFinished(request: Request): void {
    if (!requests.has(request)) return;
    pending.push(readResponse(request).catch(() => {
      const entry = requests.get(request);
      if (entry !== undefined) entry.error = "response body unavailable";
    }));
  }

  function requestFailed(request: Request): void {
    const entry = requests.get(request);
    if (entry !== undefined) entry.error = "network request failed";
  }

  page.on("request", requestStarted);
  page.on("response", responseReceived);
  page.on("requestfinished", requestFinished);
  page.on("requestfailed", requestFailed);

  return {
    async finish(): Promise<Traffic> {
      page.off("request", requestStarted);
      page.off("response", responseReceived);
      page.off("requestfinished", requestFinished);
      page.off("requestfailed", requestFailed);
      await Promise.all(pending);
      return { entries, dropped, duration_ms: Date.now() - started };
    },
  };
}
