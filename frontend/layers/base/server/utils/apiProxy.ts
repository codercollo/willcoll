import { Readable } from "node:stream";
import { appendResponseHeader, sendStream, setResponseHeader, setResponseStatus } from "h3";
import type { H3Event } from "h3";
import { buildForwardHeaders, filterResponseHeaders, resolveUpstreamUrl } from "./apiProxyCore";

// Forwards the request to an API, keeping method, headers (cookies included),
// body, query string and response headers. The target comes from
// useRuntimeConfig() at REQUEST time (NUXT_API_INTERNAL_BASE /
// NUXT_API_PLATFORM_INTERNAL_BASE), never from a build-time routeRules entry,
// so one image runs in local staging and production with only env changes.
// `stripPrefix` is the part of the incoming path the API does not have
// (/_platform).
//
// Behaviour (tests/apiProxy.test.ts):
//  - a path that is not a clean /v1/** path is refused with 400;
//  - the client address chain goes upstream in X-Forwarded-For / X-Real-IP;
//  - status, redirects (not followed), several Set-Cookie headers and the
//    body (streamed, never buffered) come back; hop-by-hop headers do not;
//  - an unreachable API is a logged 502 in the API's {"error": ...} envelope.
export async function proxyToApi(event: H3Event, base: string, stripPrefix = "") {
  const route = event.path.split("?")[0];
  const fail = (status: number, message: string) => {
    setResponseStatus(event, status);
    setResponseHeader(event, "content-type", "application/json; charset=utf-8");
    return { error: message };
  };

  if (!base) {
    console.error(JSON.stringify({ level: "ERROR", msg: "api proxy: API base is not configured", route }));
    return fail(502, "the API is not configured");
  }

  const target = resolveUpstreamUrl(event.path, base, stripPrefix);
  if (!target.ok) {
    console.warn(JSON.stringify({ level: "WARN", msg: "api proxy: refused path", route, reason: target.reason }));
    setResponseStatus(event, 400);
    setResponseHeader(event, "content-type", "application/json; charset=utf-8");
    return { error: "invalid request path" };
  }

  const req = event.node.req;
  const hasBody = !["GET", "HEAD", "OPTIONS"].includes(event.method);
  const init: RequestInit & { duplex?: "half" } = {
    method: event.method,
    headers: buildForwardHeaders(req.headers, req.socket.remoteAddress),
    redirect: "manual",
  };
  if (hasBody) {
    init.body = Readable.toWeb(req) as unknown as ReadableStream;
    init.duplex = "half";
  }

  let upstream: Response;
  try {
    upstream = await fetch(target.url, init);
  } catch (err) {
    const cause = err instanceof Error ? (err.cause as Error | undefined) : undefined;
    console.error(
      JSON.stringify({
        level: "ERROR",
        msg: "api proxy: upstream unreachable",
        route,
        upstream: new URL(target.url).origin,
        error: cause?.message || (err instanceof Error ? err.message : String(err)),
      }),
    );
    return fail(502, "the API is unreachable");
  }

  const { headers, cookies } = filterResponseHeaders(upstream.headers);
  setResponseStatus(event, upstream.status, upstream.statusText);
  for (const [name, value] of headers) setResponseHeader(event, name, value);
  for (const cookie of cookies) appendResponseHeader(event, "set-cookie", cookie);

  if (!upstream.body || event.method === "HEAD" || upstream.status === 204 || upstream.status === 304) return "";
  return sendStream(event, upstream.body as ReadableStream);
}
