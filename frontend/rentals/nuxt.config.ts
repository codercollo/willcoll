import tailwindcss from "@tailwindcss/vite";

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: "2025-07-15",
  devtools: { enabled: true },
  // Browser-rendered. API calls go same-origin (/v1) and are proxied to the API,
  // so the session cookie belongs to this origin and phones on the LAN work.
  ssr: false,
  modules: ["@pinia/nuxt", "@nuxt/fonts", "@nuxt/icon", "@nuxt/image"],
  css: ["~/assets/css/main.css"],
  vite: {
    plugins: [tailwindcss()],
    // Dev only: allow Cloudflare quick-tunnel hostnames (QR scans from phones).
    server: { allowedHosts: [".trycloudflare.com"] },
  },
  runtimeConfig: {
    // Server-only: the address the Nuxt server itself uses to reach the API
    // (never sent to the browser). Overridden by NUXT_API_INTERNAL_BASE.
    // Used both by the /v1/** proxy below and by server/routes/q/[token].get.ts,
    // which must NOT follow the API's redirect server-side (see that file).
    apiInternalBase: "http://localhost:4000",
    public: {
      // Empty = same origin (proxied below). Overridden by NUXT_PUBLIC_API_BASE.
      apiBase: "",
    },
  },
  routeRules: {
    "/v1/**": {
      proxy: `${process.env.NUXT_API_INTERNAL_BASE || "http://localhost:4000"}/v1/**`,
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
