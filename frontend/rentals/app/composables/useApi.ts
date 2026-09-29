import { ApiError, defaultMessage, parseApiError } from "~/utils/apiError";
import { flashOf } from "~/utils/flash";

export interface ApiOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  query?: Record<string, string | number | boolean | undefined | null>;
  token?: string | null; // pay-page Bearer token
  headers?: Record<string, string>;
  rawBody?: BodyInit; // CSV upload etc.
}

// Single doorway to the API (rule R1). Sends the session cookie, leaves the
// envelope for callers to read, and throws ApiError on any failure.
export function useApi() {
  const base = useRuntimeConfig().public.apiBase.replace(/\/$/, "");
  const router = useRouter();
  const nuxt = useNuxtApp(); // async callers lose the Nuxt context, so keep it

  function url(path: string, query?: ApiOptions["query"]) {
    // Empty base = same origin (Nuxt proxies /v1 to the API).
    const u = new URL(`${base}/v1${path}`, window.location.origin);
    for (const [k, v] of Object.entries(query ?? {})) {
      if (v !== undefined && v !== null && v !== "")
        u.searchParams.set(k, String(v));
    }
    return u.toString();
  }

  // A 401 on a manager route means signed out; the pay page handles its own.
  function onFailure(err: ApiError, opts: ApiOptions) {
    if (!err.isAuth || opts.token || !import.meta.client) return;
    const path = router.currentRoute.value.path;
    // Sign-in screens get their own 401 (wrong password), never a session-expired redirect.
    if (
      path.startsWith("/auth") ||
      path.startsWith("/pay/") ||
      path === "/admin/login"
    )
      return;
    nuxt.runWithContext(() => {
      const auth = useAuthStore();
      const wasAdmin = auth.principal === "admin";
      auth.expire();
      useToast().info("Your session ended. Please sign in again.");
      navigateTo(wasAdmin ? "/admin/login" : "/auth/login");
    });
  }

  // Show a flash exactly once (the API pops it), whatever the status.
  function showFlash(body: unknown) {
    const message = flashOf(body);
    if (message && import.meta.client)
      nuxt.runWithContext(() => useToast().success(message));
  }

  // 402: the free trial is over and no plan is active. Send them to pay.
  function onPaywall(err: ApiError) {
    if (err.status !== 402 || !import.meta.client) return;
    nuxt.runWithContext(() => {
      useAuthStore().markExpired();
      if (router.currentRoute.value.path !== "/billing") navigateTo("/billing");
    });
  }

  async function send(path: string, opts: ApiOptions = {}): Promise<Response> {
    const headers: Record<string, string> = { ...opts.headers };
    if (opts.token) headers.Authorization = `Bearer ${opts.token}`;
    let body: BodyInit | undefined = opts.rawBody;
    if (opts.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(opts.body);
    }
    let res: Response;
    try {
      res = await fetch(url(path, opts.query), {
        method: opts.method ?? "GET",
        headers,
        body,
        credentials: "include",
      });
    } catch {
      throw new ApiError(0, defaultMessage(0));
    }
    if (!res.ok) {
      const parsed = await res.json().catch(() => null);
      showFlash(parsed);
      const err = parseApiError(res.status, parsed);
      onFailure(err, opts);
      onPaywall(err);
      throw err;
    }
    return res;
  }

  async function request<T = Record<string, unknown>>(
    path: string,
    opts: ApiOptions = {},
  ): Promise<T> {
    const res = await send(path, opts);
    if (res.status === 204) return {} as T;
    const body = await res.json().catch(() => ({}));
    showFlash(body);
    return body as T;
  }

  async function blob(path: string, opts: ApiOptions = {}): Promise<Blob> {
    return (await send(path, opts)).blob();
  }

  // Like blob(), but also hands back the response headers — for an endpoint
  // that rides extra data alongside a binary body (e.g. a bulk PDF's
  // created/reused/skipped summary in X-Bulk-Qr-Summary), which a plain
  // blob() download throws away.
  async function blobWithHeaders(
    path: string,
    opts: ApiOptions = {},
  ): Promise<{ blob: Blob; headers: Headers }> {
    const res = await send(path, opts);
    return { blob: await res.blob(), headers: res.headers };
  }

  return {
    request,
    get: <T = Record<string, unknown>>(
      path: string,
      query?: ApiOptions["query"],
      o: ApiOptions = {},
    ) => request<T>(path, { ...o, query }),
    post: <T = Record<string, unknown>>(
      path: string,
      body?: unknown,
      o: ApiOptions = {},
    ) => request<T>(path, { ...o, method: "POST", body }),
    put: <T = Record<string, unknown>>(
      path: string,
      body?: unknown,
      o: ApiOptions = {},
    ) => request<T>(path, { ...o, method: "PUT", body }),
    patch: <T = Record<string, unknown>>(
      path: string,
      body?: unknown,
      o: ApiOptions = {},
    ) => request<T>(path, { ...o, method: "PATCH", body }),
    del: <T = Record<string, unknown>>(path: string, o: ApiOptions = {}) =>
      request<T>(path, { ...o, method: "DELETE" }),
    blob,
    blobWithHeaders,
    /** Open a PDF in a new tab (fetched with credentials, never stored). */
    async openPdf(path: string, o: ApiOptions = {}) {
      const b = await blob(path, o);
      window.open(URL.createObjectURL(b), "_blank", "noopener");
    },
    // Triggers a browser download (Content-Disposition: attachment
    // responses, e.g. CSV exports) rather than opening a new tab.
    async download(path: string, filename: string, o: ApiOptions = {}) {
      const b = await blob(path, o);
      const url = URL.createObjectURL(b);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      a.click();
      URL.revokeObjectURL(url);
    },
  };
}
