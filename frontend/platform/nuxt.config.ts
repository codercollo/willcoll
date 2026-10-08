import { PLATFORM_GUARD } from "./guard.config";

// frontend/platform: the account and admin app (app.willcoll.co.ke in
// production; build-phases.txt 3l). Sign-in, signup, account, staff, modules,
// billing and the Super Admin live here; the product apps hold only product
// pages and send anyone signed out here.
export default defineNuxtConfig({
  extends: ["../layers/base"],
  compatibilityDate: "2025-07-15",
  devtools: { enabled: true },
  // Browser-rendered, like rentals. API calls go same-origin (/_platform/v1)
  // and are proxied to platform-api, so the session cookie it sets belongs to
  // this host. In dev both apps run on `localhost` (ports differ, cookies do
  // not), so the one cookie signs you in to both. In production platform-api
  // sets it with Domain=SESSION_COOKIE_DOMAIN (e.g. .willcoll.co.ke) and every
  // app subdomain receives it.
  ssr: false,
  runtimeConfig: {
    // Server-only: where the /_platform/v1/** proxy (layers/base server/routes)
    // sends requests. Read at request time; NUXT_API_PLATFORM_INTERNAL_BASE.
    apiPlatformInternalBase: "http://localhost:4100",
    public: {
      // Platform is not a product: no per-product billing rewrite, no paywall.
      product: "",
      guard: PLATFORM_GUARD,
    },
  },
  icon: { serverBundle: "local" },
  app: {
    head: {
      htmlAttrs: { lang: "en" },
      title: "Willcoll",
      meta: [{ name: "viewport", content: "width=device-width, initial-scale=1" }],
    },
  },
});
