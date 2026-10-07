import type { GuardConfig } from "../layers/base/app/utils/routeGuard";

// Rentals' sign-in guard paths, in full. nuxt.config.ts hands this to the base
// layer's middleware (runtimeConfig.public.guard); tests/unitCode.test.ts
// asserts against the same object, so the two cannot drift.
//
// Sign-in, the Super Admin and billing are NOT Rentals pages any more
// (build-phases.txt 3l): loginMode 'platform' makes the middleware send a
// signed-out visitor to the platform app's login with ?return_to=<this page>.
// loginPath/adminLoginPath/paywallAllowed[0] are therefore paths on the
// PLATFORM app, used as signals for utils/routeGuard.ts platformRedirect().
export const RENTALS_GUARD: GuardConfig = {
  managerHome: "/properties",
  adminHome: "/admin/managers",
  loginPath: "/auth/login",
  adminLoginPath: "/admin/login",
  adminArea: "/admin",
  // No sign-in pages live here.
  signInPaths: [],
  // The Rentals design-system sheet is reachable signed out.
  publicPaths: ["/_design"],
  // The tenant pay page never needs (or waits for) a session.
  openPaths: ["/pay"],
  // "/billing" is platform's page for this product (platformRedirect maps it).
  paywallAllowed: ["/billing"],
  loginMode: "platform",
};
