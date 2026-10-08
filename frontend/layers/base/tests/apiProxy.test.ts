// @vitest-environment node
//
// The whole proxy over real sockets: a browser-side http client -> an h3 app
// running proxyToApi -> a stand-in upstream http server.
import { createHash } from "node:crypto";
import http from "node:http";
import type { AddressInfo } from "node:net";
import { gzipSync } from "node:zlib";
import { createApp, defineEventHandler, toNodeListener } from "h3";
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { proxyToApi } from "../server/utils/apiProxy";

interface Seen {
  method?: string;
  url?: string;
  headers: http.IncomingHttpHeaders;
  body: string;
}

let upstream: http.Server;
let upstreamBase = "";
let seen: Seen[] = [];
let upstreamHandler: (req: http.IncomingMessage, res: http.ServerResponse) => void;

let proxy: http.Server;
let proxyPort = 0;
let currentBase = "";

const listen = (srv: http.Server) =>
  new Promise<number>((resolve) => srv.listen(0, "127.0.0.1", () => resolve((srv.address() as AddressInfo).port)));

beforeAll(async () => {
  upstream = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      seen.push({ method: req.method, url: req.url, headers: req.headers, body: Buffer.concat(chunks).toString() });
      upstreamHandler(req, res);
    });
  });
  upstreamBase = `http://127.0.0.1:${await listen(upstream)}`;
  currentBase = upstreamBase;

  const root = createApp();
  root.use(defineEventHandler((event) => proxyToApi(event, currentBase)));
  proxy = http.createServer(toNodeListener(root));
  proxyPort = await listen(proxy);
});

afterAll(() => {
  upstream.close();
  proxy.close();
});

afterEach(() => {
  seen = [];
  currentBase = upstreamBase;
  vi.restoreAllMocks();
});

interface Reply {
  status: number;
  headers: http.IncomingHttpHeaders;
  rawHeaders: string[];
  body: Buffer;
}

// A raw client: node does not normalise the path, so "..", "%2e%2e" and "%2f"
// reach the proxy exactly as written.
function call(path: string, opts: { method?: string; headers?: Record<string, string>; body?: string } = {}): Promise<Reply> {
  return new Promise((resolve, reject) => {
    const req = http.request({ host: "127.0.0.1", port: proxyPort, path, method: opts.method || "GET", headers: opts.headers }, (res) => {
      const chunks: Buffer[] = [];
      res.on("data", (c) => chunks.push(c));
      res.on("end", () => resolve({ status: res.statusCode!, headers: res.headers, rawHeaders: res.rawHeaders, body: Buffer.concat(chunks) }));
    });
    req.on("error", reject);
    if (opts.body) req.write(opts.body);
    req.end();
  });
}

describe("path safety", () => {
  const bad = [
    "/v1/../admin",
    "/v1/%2e%2e/admin",
    "/v1/%2E%2E/admin",
    "/v1/..%2fadmin",
    "/v1/a%2Fb",
    "/v1/%252e%252e/admin",
    "/admin",
    "/v1x",
    "//evil.example/v1/x",
  ];
  it.each(bad)("%s is a 400 and never reaches the API", async (path) => {
    upstreamHandler = (_req, res) => res.end("reached");
    const r = await call(path);
    expect(r.status).toBe(400);
    expect(JSON.parse(r.body.toString())).toEqual({ error: "invalid request path" });
    expect(seen).toHaveLength(0);
  });

  it("a clean path goes through with its query string", async () => {
    upstreamHandler = (_req, res) => res.end("reached");
    const r = await call("/v1/properties?x=1&y=a%20b");
    expect(r.status).toBe(200);
    expect(seen[0].url).toBe("/v1/properties?x=1&y=a%20b");
  });
});

describe("client address", () => {
  it("forwards X-Forwarded-For with the peer appended and X-Real-IP as received", async () => {
    upstreamHandler = (_req, res) => res.end("ok");
    await call("/v1/x", { headers: { "X-Forwarded-For": "203.0.113.7", "X-Real-IP": "203.0.113.7" } });
    expect(seen[0].headers["x-forwarded-for"]).toBe("203.0.113.7, 127.0.0.1");
    expect(seen[0].headers["x-real-ip"]).toBe("203.0.113.7");
  });

  it("starts the chain at the peer when the request had none", async () => {
    upstreamHandler = (_req, res) => res.end("ok");
    await call("/v1/x");
    expect(seen[0].headers["x-forwarded-for"]).toBe("127.0.0.1");
  });
});

describe("pass-through", () => {
  it.each([201, 204, 400, 404, 409, 422, 429, 500, 503])("status %i", async (status) => {
    upstreamHandler = (_req, res) => {
      res.statusCode = status;
      res.setHeader("content-type", "application/json");
      res.end(status === 204 ? undefined : JSON.stringify({ error: "e" }));
    };
    const r = await call("/v1/x");
    expect(r.status).toBe(status);
    if (status !== 204) expect(JSON.parse(r.body.toString())).toEqual({ error: "e" });
  });

  it("sends several Set-Cookie headers as separate headers", async () => {
    upstreamHandler = (_req, res) => {
      res.setHeader("Set-Cookie", [
        "willcoll_session=abc; Path=/; Domain=.willcoll.localhost; Secure; HttpOnly; SameSite=Lax",
        "flash=1; Path=/; Expires=Wed, 21 Oct 2026 07:28:00 GMT",
      ]);
      res.end("ok");
    };
    const r = await call("/v1/x");
    expect(r.headers["set-cookie"]).toEqual([
      "willcoll_session=abc; Path=/; Domain=.willcoll.localhost; Secure; HttpOnly; SameSite=Lax",
      "flash=1; Path=/; Expires=Wed, 21 Oct 2026 07:28:00 GMT",
    ]);
  });

  it("returns a redirect with its Location instead of following it", async () => {
    upstreamHandler = (req, res) => {
      if (req.url === "/v1/q/tok") {
        res.statusCode = 302;
        res.setHeader("Location", "/pay/runda-arcade/G1");
        res.end();
        return;
      }
      res.end("followed");
    };
    const r = await call("/v1/q/tok");
    expect(r.status).toBe(302);
    expect(r.headers.location).toBe("/pay/runda-arcade/G1");
    expect(seen).toHaveLength(1);
  });

  it("forwards request method, body, cookies and content type", async () => {
    upstreamHandler = (_req, res) => res.end("ok");
    await call("/v1/payments", {
      method: "POST",
      headers: { "Content-Type": "application/json", Cookie: "willcoll_session=abc" },
      body: JSON.stringify({ amount: "100.00" }),
    });
    expect(seen[0]).toMatchObject({ method: "POST", body: '{"amount":"100.00"}' });
    expect(seen[0].headers.cookie).toBe("willcoll_session=abc");
    expect(seen[0].headers["content-type"]).toBe("application/json");
  });

  it("streams a large download without changing a byte", async () => {
    const total = 40 * 1024 * 1024;
    const hash = createHash("sha256");
    const chunk = Buffer.alloc(1024 * 1024, "xlsx-or-pdf-bytes");
    let sent = 0;
    for (let i = 0; i < total / chunk.length; i++) hash.update(chunk);
    const want = hash.digest("hex");

    upstreamHandler = (_req, res) => {
      res.setHeader("Content-Type", "application/pdf");
      res.setHeader("Content-Disposition", 'attachment; filename="report.pdf"');
      res.setHeader("Content-Length", String(total));
      const write = () => {
        while (sent < total) {
          sent += chunk.length;
          if (!res.write(chunk)) return void res.once("drain", write);
        }
        res.end();
      };
      write();
    };
    const r = await call("/v1/reports/2026-09/pdf");
    expect(r.status).toBe(200);
    expect(r.headers["content-type"]).toBe("application/pdf");
    expect(r.headers["content-disposition"]).toBe('attachment; filename="report.pdf"');
    expect(r.body.length).toBe(total);
    expect(createHash("sha256").update(r.body).digest("hex")).toBe(want);
  }, 60_000);

  it("delivers a gzip response decoded and consistent", async () => {
    upstreamHandler = (_req, res) => {
      const z = gzipSync("hello hello hello");
      res.setHeader("Content-Encoding", "gzip");
      res.setHeader("Content-Length", String(z.length));
      res.end(z);
    };
    const r = await call("/v1/x");
    expect(r.body.toString()).toBe("hello hello hello");
    expect(r.headers["content-encoding"]).toBeUndefined();
  });

  it("does not forward hop-by-hop request headers and drops them from responses", async () => {
    upstreamHandler = (_req, res) => {
      res.setHeader("Connection", "x-hop");
      res.setHeader("X-Hop", "1");
      res.setHeader("Keep-Alive", "timeout=5");
      res.setHeader("Proxy-Authenticate", "Basic");
      res.setHeader("X-Kept", "yes");
      res.end("ok");
    };
    const r = await call("/v1/x", {
      headers: { Connection: "keep-alive, X-Secret-Hop", "X-Secret-Hop": "1", TE: "trailers", "Proxy-Authorization": "Basic x" },
    });
    expect(seen[0].headers["x-secret-hop"]).toBeUndefined();
    expect(seen[0].headers.te).toBeUndefined();
    expect(seen[0].headers["proxy-authorization"]).toBeUndefined();
    expect(r.headers["x-hop"]).toBeUndefined();
    expect(r.headers["proxy-authenticate"]).toBeUndefined();
    expect(r.headers["x-kept"]).toBe("yes");
  });
});

describe("upstream down", () => {
  it("answers 502 in the API error envelope and logs it", async () => {
    const dead = http.createServer();
    const port = await listen(dead);
    await new Promise((r) => dead.close(r)); // nothing listens here any more
    currentBase = `http://127.0.0.1:${port}`;

    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const r = await call("/v1/properties");

    expect(r.status).toBe(502);
    expect(r.headers["content-type"]).toContain("application/json");
    expect(JSON.parse(r.body.toString())).toEqual({ error: "the API is unreachable" });
    expect(log).toHaveBeenCalledTimes(1);
    const entry = JSON.parse(String(log.mock.calls[0][0]));
    expect(entry).toMatchObject({ level: "ERROR", msg: "api proxy: upstream unreachable", route: "/v1/properties" });
  });

  it("answers 502 when the API base is not configured", async () => {
    currentBase = "";
    vi.spyOn(console, "error").mockImplementation(() => {});
    const r = await call("/v1/properties");
    expect(r.status).toBe(502);
    expect(JSON.parse(r.body.toString())).toEqual({ error: "the API is not configured" });
  });
});
