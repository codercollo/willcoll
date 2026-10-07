import type { Access, Principal } from '../types/session'
import { appUrl, billingUrl, loginUrl, type AppUrls } from './appLinks'

// What differs per product: where each kind of visitor lands, and which paths
// are open. The base layer defaults live in layers/base/nuxt.config.ts; each
// app overrides them in its own runtimeConfig.public.guard.
export interface GuardConfig {
  managerHome: string
  adminHome: string
  loginPath: string
  adminLoginPath: string
  /** Super Admin pages live under this prefix. */
  adminArea: string
  /** Sign-in pages: open signed out, pointless signed in. */
  signInPaths: string[]
  /** Other pages reachable signed out. */
  publicPaths: string[]
  /** Never gated, signed in or not (e.g. the tenant pay page). Exact or `<path>/...`. */
  openPaths: string[]
  /**
   * All a firm whose trial ended may reach: the pages needed to pay. In a
   * product app those pages live in platform, so the first entry ("/billing")
   * is only a signal that platformRedirect() turns into platform's billing page.
   */
  paywallAllowed: string[]
  /**
   * Where the sign-in, Super Admin and billing pages live. 'local' (platform
   * itself): in this app. 'platform' (every product app, and the default): in
   * the platform app, reached with a full-page redirect carrying return_to.
   */
  loginMode?: 'local' | 'platform'
}

const isOpen = (path: string, cfg: GuardConfig) => cfg.openPaths.some(p => path === p || path.startsWith(p + '/'))
const under = (path: string, prefixes: string[]) => prefixes.some(p => path.startsWith(p))

// Where should this visitor go? Returns null to allow the navigation.
// Pure so it can be unit tested without a router.
export function guardRedirect(path: string, principal: Principal, access: Access['state'] | null | undefined, cfg: GuardConfig): string | null {
  const isPublic = under(path, cfg.signInPaths) || under(path, cfg.publicPaths) || isOpen(path, cfg)
  const isAdminArea = path === cfg.adminArea || path.startsWith(cfg.adminArea + '/')
  const home = principal === 'admin' ? cfg.adminHome : cfg.managerHome

  if (path === '/') return principal ? home : cfg.loginPath
  if (isOpen(path, cfg)) return null // tenants never sign in

  if (!principal) {
    if (isPublic) return null
    return isAdminArea ? cfg.adminLoginPath : cfg.loginPath
  }
  // Signed in: the sign-in pages are pointless.
  if (under(path, cfg.signInPaths)) return home
  if (isAdminArea && principal !== 'admin') return cfg.managerHome
  // The paywall: a firm whose trial ended and has no plan can only reach the
  // pages needed to pay (and its account).
  if (principal === 'manager' && access === 'expired' && !under(path, cfg.paywallAllowed)) return cfg.paywallAllowed[0] ?? cfg.managerHome
  if (!isAdminArea && principal === 'admin' && !isPublic) return cfg.adminHome
  return null
}

/**
 * In a product app, some guard targets are pages that now live in the platform
 * app. Returns the absolute URL to send the browser to, or null when `target`
 * is an ordinary page of this app (use the router).
 *
 * `here` is this page's own absolute URL: sign-in remembers it as return_to so
 * the person lands back where they started.
 */
export function platformRedirect(
  target: string,
  principal: Principal,
  cfg: GuardConfig,
  apps: Partial<AppUrls>,
  product: string,
  here: string,
): string | null {
  if ((cfg.loginMode ?? 'platform') !== 'platform') return null
  if (target === cfg.loginPath) return loginUrl(apps, here)
  if (target === cfg.adminLoginPath) return appUrl(apps, 'platform', cfg.adminLoginPath)
  // A Super Admin has no product pages: their home is platform's admin area.
  if (principal === 'admin' && target === cfg.adminHome) return appUrl(apps, 'platform', cfg.adminHome)
  if (target === cfg.paywallAllowed[0]) return billingUrl(apps, product)
  return null
}
