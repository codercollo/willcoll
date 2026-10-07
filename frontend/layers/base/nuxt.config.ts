import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";

// @willcoll/base: the product-agnostic frontend layer (build-phases.txt 3k).
// Components, composables, utils, the design tokens and the auth middleware
// every product app (rentals, estates, waste, platform) extends. No app.vue,
// no pages: those belong to the product. Product-specific values (which
// product this is, where a signed-in visitor lands) come from the app's own
// runtimeConfig, never from this layer.
export default defineNuxtConfig({
  modules: ["@pinia/nuxt", "@nuxt/fonts", "@nuxt/icon", "@nuxt/image"],
  css: [fileURLToPath(new URL("./app/assets/css/main.css", import.meta.url))],
  // How a product app (and its tests) reaches the layer explicitly: `#base/utils/money`.
  alias: { "#base": fileURLToPath(new URL("./app", import.meta.url)) },
  vite: { plugins: [tailwindcss()] },
  runtimeConfig: {
    public: {
      // Empty = same origin (the app proxies /v1). NUXT_PUBLIC_API_BASE.
      apiBase: "",
      // Where the browser reaches platform-api (identity, account, billing,
      // admin oversight). NUXT_PUBLIC_API_PLATFORM_BASE.
      apiPlatformBase: "/_platform",
      // The product this app is ("rentals", ...): platform-api's per-product
      // billing routes and the session's trial/paywall standing key on it.
      product: "",
      // Origins of the Willcoll apps (build-phases.txt 3l): where the product
      // switcher, the sign-in redirect and the billing links point, and the
      // ONLY origins a sign-in may return to (utils/appLinks.ts safeReturnTo).
      // Dev: localhost ports (cookies are shared across ports of one host).
      // Production: subdomains, e.g. NUXT_PUBLIC_APPS_PLATFORM=https://app.willcoll.co.ke.
      // An empty value means that app is not deployed (no switcher entry).
      apps: {
        platform: "http://localhost:3001",
        rentals: "http://localhost:3000",
        estates: "",
        waste: "",
      },
      // Where the sign-in guard sends people (utils/routeGuard.ts). Each
      // product overrides what differs.
      guard: {
        managerHome: "/",
        adminHome: "/",
        loginPath: "/auth/login",
        adminLoginPath: "/admin/login",
        adminArea: "/admin",
        signInPaths: ["/auth", "/admin/login"],
        publicPaths: [] as string[],
        openPaths: [] as string[],
        paywallAllowed: ["/billing", "/account"],
        // 'platform' (default): sign-in/admin/billing pages are in the platform
        // app. The platform app sets 'local'.
        loginMode: "platform" as "local" | "platform",
      },
    },
  },
});
