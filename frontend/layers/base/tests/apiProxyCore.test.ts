// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  buildForwardHeaders,
  filterResponseHeaders,
  resolveUpstreamUrl,
} from "../server/utils/apiProxyCore";

const BASE = "http://rentals-api:4000";

describe("resolveUpstreamUrl: accepted paths", () => {
  it.each([
    ["/v1/properties", "", "http://rentals-api:4000/v1/properties"],
    ["/v1/properties?x=1&y=a%20b", "", "http://rentals-api:4000/v1/properties?x=1&y=a%20b"],
    ["/v1/pay/runda-arcade/G1", "", "http://rentals-api:4000/v1/pay/runda-arcade/G1"],
    ["/v1/units/a%20b", "", "http://rentals-api:4000/v1/units/a%20b"],
    ["/_platform/v1/me", "/_platform", "http://rentals-api:4000/v1/me"],
    ["/v1", "", "http://rentals-api:4000/v1"],
  ])("%s", (path, prefix, want) => {
    expect(resolveUpstreamUrl(path, BASE, prefix)).toEqual({ ok: true, url: want });
  });

  it("tolerates a trailing slash on the base", () => {
    expect(resolveUpstreamUrl("/v1/x", BASE + "/")).toEqual({ ok: true, url: BASE + "/v1/x" });
  });
});

describe("resolveUpstreamUrl: refused paths", () => {
  const refused: [string, string][] = [
    ["/v1/../admin", ""],
    ["/v1/properties/../../admin", ""],
    ["/v1/%2e%2e/admin", ""],
    ["/v1/%2E%2E/admin", ""],
    ["/v1/.%2e/admin", ""],
    ["/v1/%2e./admin", ""],
    ["/v1/..%2fadmin", ""],
    ["/v1/..%2Fadmin", ""],
    ["/v1/%2e%2e%2fadmin", ""],
    ["/v1/a%2fb", ""],
    ["/v1/a%2Fb", ""],
    ["/v1/a%5cb", ""],
    ["/v1/a\\b", ""],
    ["/v1/%252e%252e/admin", ""], // double encoded ..
    ["/v1/%252fadmin", ""], // double encoded /
    ["/v1/a%00b", ""],
    ["/v1/./x", ""],
    ["/v1//x", ""],
    ["/v1/%", ""], // malformed escape
    ["/admin", ""],
    ["/v1x/foo", ""],
    ["/v2/foo", ""],
    ["//evil.example/v1/x", ""],
    ["/_platform/v1/..%2f..%2fadmin", "/_platform"],
    ["/_platform/v1/%2e%2e/x", "/_platform"],
    ["/_platform/admin", "/_platform"],
    ["/v1/foo", "/_platform"], // not under the prefix
    ["/_platformx/v1/foo", "/_platform"],
  ];
  it.each(refused)("%s (prefix %j)", (path, prefix) => {
    expect(resolveUpstreamUrl(path, BASE, prefix).ok).toBe(false);
  });

  it("a .. in the query string is not a path escape", () => {
    expect(resolveUpstreamUrl("/v1/files?name=../x", BASE).ok).toBe(true);
  });
});

describe("buildForwardHeaders", () => {
  it("appends the direct peer to the incoming X-Forwarded-For chain", () => {
    const h = buildForwardHeaders({ "x-forwarded-for": "203.0.113.7, 10.0.0.2" }, "172.18.0.5");
    expect(h["x-forwarded-for"]).toBe("203.0.113.7, 10.0.0.2, 172.18.0.5");
  });

  it("starts the chain with the peer when there was none, unmapping ::ffff:", () => {
    expect(buildForwardHeaders({}, "::ffff:192.168.1.9")["x-forwarded-for"]).toBe("192.168.1.9");
  });

  it("joins a repeated X-Forwarded-For header", () => {
    const h = buildForwardHeaders({ "x-forwarded-for": ["1.1.1.1", "2.2.2.2"] }, "10.0.0.1");
    expect(h["x-forwarded-for"]).toBe("1.1.1.1, 2.2.2.2, 10.0.0.1");
  });

  it("forwards X-Real-IP, cookies and authorization untouched", () => {
    const h = buildForwardHeaders(
      { "x-real-ip": "203.0.113.7", cookie: "a=1; b=2", authorization: "Bearer t", "content-type": "application/json" },
      "10.0.0.1",
    );
    expect(h).toMatchObject({ "x-real-ip": "203.0.113.7", cookie: "a=1; b=2", authorization: "Bearer t", "content-type": "application/json" });
  });

  it("strips hop-by-hop headers and headers named by Connection", () => {
    const h = buildForwardHeaders(
      {
        connection: "keep-alive, X-Secret-Hop",
        "keep-alive": "timeout=5",
        "x-secret-hop": "1",
        te: "trailers",
        upgrade: "websocket",
        "transfer-encoding": "chunked",
        "proxy-authorization": "Basic x",
        host: "rentals.willcoll.localhost",
        "content-length": "5",
        "x-kept": "yes",
      },
      "10.0.0.1",
    );
    for (const gone of ["connection", "keep-alive", "x-secret-hop", "te", "upgrade", "transfer-encoding", "proxy-authorization", "host", "content-length"]) {
      expect(h).not.toHaveProperty(gone);
    }
    expect(h["x-kept"]).toBe("yes");
  });

  it("asks upstream for an uncompressed body", () => {
    expect(buildForwardHeaders({ "accept-encoding": "gzip, br" }, "10.0.0.1")["accept-encoding"]).toBe("identity");
  });
});

describe("filterResponseHeaders", () => {
  it("keeps every Set-Cookie as its own value", () => {
    const up = new Headers();
    up.append("set-cookie", "a=1; Path=/; Expires=Wed, 21 Oct 2026 07:28:00 GMT");
    up.append("set-cookie", "b=2; Path=/; HttpOnly");
    const r = filterResponseHeaders(up);
    expect(r.cookies).toEqual(["a=1; Path=/; Expires=Wed, 21 Oct 2026 07:28:00 GMT", "b=2; Path=/; HttpOnly"]);
    expect(r.headers.map(([n]) => n)).not.toContain("set-cookie");
  });

  it("drops hop-by-hop headers and those named by Connection", () => {
    const up = new Headers({
      connection: "close, x-hop",
      "x-hop": "1",
      "keep-alive": "timeout=5",
      "proxy-authenticate": "Basic",
      "transfer-encoding": "chunked",
      location: "/pay/x/1",
      "x-request-id": "r1",
    });
    const names = filterResponseHeaders(up).headers.map(([n]) => n);
    expect(names).toEqual(expect.arrayContaining(["location", "x-request-id"]));
    for (const gone of ["connection", "x-hop", "keep-alive", "proxy-authenticate", "transfer-encoding"]) {
      expect(names).not.toContain(gone);
    }
  });

  it("drops content-encoding and content-length together (fetch decodes the body)", () => {
    const names = filterResponseHeaders(new Headers({ "content-encoding": "gzip", "content-length": "20", etag: "x" })).headers.map(([n]) => n);
    expect(names).toEqual(["etag"]);
  });

  it("keeps content-length when the body was not encoded", () => {
    const names = filterResponseHeaders(new Headers({ "content-length": "20" })).headers.map(([n]) => n);
    expect(names).toEqual(["content-length"]);
  });
});
