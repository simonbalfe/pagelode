import type { BrowserContext } from "patchright";
import type { Cookie } from "../shared/schemas.ts";

const replayHeaderNames = ["user-agent", "accept", "accept-language", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform"];

export type Replay = {
  identity?: string;
  proxy_url?: string;
  headers: Record<string, string>;
};

export function replayHeaders(requestHeaders: Record<string, string>): Record<string, string> {
  return Object.fromEntries(replayHeaderNames.flatMap((name) => requestHeaders[name] === undefined ? [] : [[name, requestHeaders[name]]]));
}

export async function portableCookies(context: BrowserContext, url?: string): Promise<Cookie[]> {
  return (await context.cookies(url)).map((cookie) => ({
    name: cookie.name,
    value: cookie.value,
    domain: cookie.domain,
    path: cookie.path,
    expires: cookie.expires,
    http_only: cookie.httpOnly,
    secure: cookie.secure,
    same_site: cookie.sameSite,
  }));
}
