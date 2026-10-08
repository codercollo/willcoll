import { RENTALS_GUARD } from "./guard.config";

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  // Components, composables, utils, design tokens and the auth middleware
  // shared by every product (build-phases.txt 3k). Modules, Tailwind and the
  // base CSS come with it.
  extends: ["../layers/base"],
  compatibilityDate: "2025-07-15",
  devtools: { enabled: true },
  // Browser-rendered. API calls go same-origin (/v1) and are proxied to the API,
  // so the session cookie belongs to this origin and phones on the LAN work.
  ssr: false,
  vite: {
    // Dev only: allow Cloudflare quick-tunnel hostnames (QR scans from phones).
    server: { allowedHosts: [".trycloudflare.com"] },
  },
  runtimeConfig: {
    // Server-only: the address the Nuxt server itself uses to reach the API
    // (never sent to the browser). Overridden by NUXT_API_INTERNAL_BASE.
    // Used both by the /v1/** proxy (server/routes/v1) and by server/routes/q/[token].get.ts,
    // which must NOT follow the API's redirect server-side (see that file).
    apiInternalBase: "http://localhost:4000",
    // Same for platform-api (identity, account, billing, admin oversight),
    // behind the /_platform/v1/** proxy (layers/base server/routes). NUXT_API_PLATFORM_INTERNAL_BASE.
    apiPlatformInternalBase: "http://localhost:4100",
    public: {
      // Empty = same origin (proxied below). Overridden by NUXT_PUBLIC_API_BASE.
      apiBase: "",
      // Where the browser reaches platform-api. Default: the same-origin
      // /_platform proxy, so the platform session cookie is first-party
      // to this app and rentals-api (proxied the same way) receives it.
      // Overridden by NUXT_PUBLIC_API_PLATFORM_BASE (e.g. https://api.willcoll.co.ke).
      apiPlatformBase: "/_platform",
      // platform-api's per-product billing routes and the session's trial /
      // paywall standing key on this.
      product: "rentals",
      // The sign-in guard (layers/base utils/routeGuard.ts), Rentals' paths.
      guard: RENTALS_GUARD,
    },
  },
  icon: { serverBundle: "local" },
  app: {
    head: {
      htmlAttrs: { lang: "en" },
      title: "Willcoll",
      meta: [
        { name: "viewport", content: "width=device-width, initial-scale=1" },
      ],
    },
  },
});
