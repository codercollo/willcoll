import type { H3Event } from "h3";

// Forwards the request to an API, keeping method, headers (cookies included),
// body, query string and response headers. The target comes from
// useRuntimeConfig() at REQUEST time (NUXT_API_INTERNAL_BASE /
// NUXT_API_PLATFORM_INTERNAL_BASE), never from a build-time routeRules entry,
// so one image runs in local staging and production with only env changes.
// `stripPrefix` is the part of the incoming path the API does not have
// (/_platform).
export function proxyToApi(event: H3Event, base: string, stripPrefix = "") {
  if (!base) {
    throw createError({ statusCode: 502, statusMessage: "API base is not configured" });
  }
  const path = event.path.startsWith(stripPrefix) ? event.path.slice(stripPrefix.length) : event.path;
  return proxyRequest(event, base.replace(/\/+$/, "") + path);
}
