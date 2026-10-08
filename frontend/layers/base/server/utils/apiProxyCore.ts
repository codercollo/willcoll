// The pure, framework-free half of the API proxy (apiProxy.ts): path safety,
// the headers that go upstream and the headers that come back. Kept free of
// h3/Nitro so it is unit-tested directly (tests/apiProxyCore.test.ts).

export type UpstreamResult = { ok: true; url: string } | { ok: false; reason: string };

// RFC 9110 7.6.1: meaningful to one connection only, never forwarded.
export const HOP_BY_HOP = [
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
];

const MAX_DECODE_ROUNDS = 4;

// Maps the incoming request path (/v1/... or <stripPrefix>/v1/..., query
// included) to the upstream URL, or refuses it. The API surface is exactly
// /v1/**. A path is refused if, once the prefix is removed, it does not start
// with /v1/, or if at any level of percent-decoding it holds a "." or ".."
// segment, an encoded slash or backslash, a NUL, or an empty segment.
export function resolveUpstreamUrl(rawPath: string, base: string, stripPrefix = ""): UpstreamResult {
  const q = rawPath.indexOf("?");
  const pathPart = q === -1 ? rawPath : rawPath.slice(0, q);
  const query = q === -1 ? "" : rawPath.slice(q);

  if (stripPrefix && !pathPart.startsWith(stripPrefix + "/")) {
    return { ok: false, reason: "outside the proxied prefix" };
  }
  const path = pathPart.slice(stripPrefix.length);
  if (path !== "/v1" && !path.startsWith("/v1/")) return { ok: false, reason: "outside /v1" };

  // Check every decoding level, so %252e%252e (double encoded) is caught too.
  let current = path;
  for (let round = 0; ; round++) {
    if (/%2f|%5c|%00/i.test(current) || current.includes("\\") || current.includes("\0")) {
      return { ok: false, reason: "encoded separator" };
    }
    if (current.includes("..") || current.split("/").some((seg) => seg === ".")) {
      return { ok: false, reason: "dot segment" };
    }
    if (current.includes("//")) return { ok: false, reason: "empty segment" };
    if (!current.includes("%")) break;
    if (round >= MAX_DECODE_ROUNDS) return { ok: false, reason: "excessive encoding" };
    let decoded: string;
    try {
      decoded = decodeURIComponent(current);
    } catch {
      return { ok: false, reason: "malformed encoding" };
    }
    if (decoded === current) break;
    current = decoded;
  }

  const baseUrl = base.replace(/\/+$/, "");
  let url: URL;
  let baseOrigin: string;
  try {
    baseOrigin = new URL(baseUrl).origin;
    url = new URL(baseUrl + path + query);
  } catch {
    return { ok: false, reason: "bad url" };
  }
  // Belt and braces: whatever the URL parser made of it must still be the
  // configured API origin and a /v1 path.
  if (url.origin !== baseOrigin || !(url.pathname === "/v1" || url.pathname.startsWith("/v1/"))) {
    return { ok: false, reason: "escapes the API" };
  }
  return { ok: true, url: url.toString() };
}

type IncomingHeaders = Record<string, string | string[] | undefined>;

function connectionTokens(h: IncomingHeaders): string[] {
  const c = h["connection"];
  const v = Array.isArray(c) ? c.join(",") : c || "";
  return v.split(",").map((t) => t.trim().toLowerCase()).filter(Boolean);
}

function stripMappedV4(ip: string): string {
  return ip.startsWith("::ffff:") ? ip.slice(7) : ip;
}

// Headers for the upstream request: everything the browser sent except
// hop-by-hop headers (and any header the Connection header names), host and
// the framing headers fetch recomputes, plus the client address chain.
//
// X-Forwarded-For is the incoming chain with the direct peer appended. The
// API, not this proxy, decides how much of that chain to believe: it honours
// it only when ITS direct peer (this server) is in TRUSTED_PROXIES. X-Real-IP
// is forwarded as received. Responses are requested uncompressed so the body
// can be streamed through as-is.
export function buildForwardHeaders(incoming: IncomingHeaders, peer: string | undefined): Record<string, string> {
  const drop = new Set([...HOP_BY_HOP, ...connectionTokens(incoming), "host", "content-length", "accept-encoding", "expect"]);
  const out: Record<string, string> = {};
  for (const [name, value] of Object.entries(incoming)) {
    const key = name.toLowerCase();
    if (value === undefined || drop.has(key) || key === "x-forwarded-for") continue;
    out[key] = Array.isArray(value) ? value.join(", ") : value;
  }
  out["accept-encoding"] = "identity";

  const prior = incoming["x-forwarded-for"];
  const chain = Array.isArray(prior) ? prior.join(", ") : prior;
  const hop = peer ? stripMappedV4(peer) : "";
  const xff = [chain, hop].filter(Boolean).join(", ");
  if (xff) out["x-forwarded-for"] = xff;
  return out;
}

export interface ForwardedResponse {
  headers: [string, string][];
  cookies: string[];
}

// Response headers to send back: no hop-by-hop headers, no content-encoding
// or content-length when fetch decoded the body, and Set-Cookie kept as
// separate values (they must never be comma-joined).
export function filterResponseHeaders(upstream: Headers): ForwardedResponse {
  const drop = new Set(HOP_BY_HOP);
  for (const t of (upstream.get("connection") || "").split(",")) {
    if (t.trim()) drop.add(t.trim().toLowerCase());
  }
  const decoded = upstream.has("content-encoding");
  const headers: [string, string][] = [];
  upstream.forEach((value, name) => {
    const key = name.toLowerCase();
    if (key === "set-cookie" || drop.has(key)) return;
    if (decoded && (key === "content-encoding" || key === "content-length")) return;
    headers.push([key, value]);
  });
  return { headers, cookies: upstream.getSetCookie() };
}
