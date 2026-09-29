// Unit QR codes point here (GET /q/:token). The API's own handler
// (scanUnitQRHandler) responds with a 302 to /pay/<slug>/<unit> — this must
// reach the phone's browser as a real redirect, not be followed here: the
// nuxt.config.ts routeRules proxy for /v1/** follows redirects transparently,
// which previously turned this into a 200 HTML response still under the
// /q/<token> URL, so the SPA's auth.global middleware (only /pay is exempt)
// bounced the phone to /auth/login instead of the pay page.
export default defineEventHandler(async (event) => {
  const token = getRouterParam(event, "token");
  if (!token) {
    throw createError({ statusCode: 400, statusMessage: "Missing token" });
  }

  const { apiInternalBase } = useRuntimeConfig(event);
  const url = `${apiInternalBase}/q/${encodeURIComponent(token)}`;

  let upstream: Response;
  try {
    upstream = await fetch(url, { redirect: "manual" });
  } catch {
    setResponseStatus(event, 502);
    setResponseHeader(event, "content-type", "text/plain");
    return "the API is unreachable";
  }

  const location = upstream.headers.get("location");
  if (location) {
    return sendRedirect(event, location, 302);
  }

  setResponseStatus(event, upstream.status, upstream.statusText);
  const contentType = upstream.headers.get("content-type");
  if (contentType) setResponseHeader(event, "content-type", contentType);
  return upstream.body;
});
