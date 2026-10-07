import type { GuardConfig } from "../layers/base/app/utils/routeGuard";

// Platform's sign-in guard paths, in full (same pattern as rentals/guard.config.ts).
// Platform owns the sign-in pages, so loginMode is 'local': a signed-out visitor
// to any page lands on /auth/login here, and a signed-in one on / (their product
// cards) or, for a Super Admin, /admin/managers.
export const PLATFORM_GUARD: GuardConfig = {
  managerHome: "/",
  adminHome: "/admin/managers",
  loginPath: "/auth/login",
  adminLoginPath: "/admin/login",
  adminArea: "/admin",
  // Every /auth/* page (signup, activate, activate-staff, forgot/reset
  // password) is open signed out and pointless signed in.
  signInPaths: ["/auth", "/admin/login"],
  publicPaths: [],
  openPaths: [],
  // Platform has no paywall of its own: billing is where an expired firm pays.
  paywallAllowed: ["/billing", "/account"],
  loginMode: "local",
};
