// How the Willcoll apps (platform, rentals, later estates and waste) link to
// each other (build-phases.txt 3l). Pure and import-free so the sign-in guard,
// the product switcher and the tests can all use it without a Nuxt context.
//
// Every app's origin comes from runtimeConfig.public.apps (dev: localhost
// ports; production: subdomains). That same list is the ONLY allow-list for
// `return_to`: a sign-in may send the browser back to an app of ours, never to
// an arbitrary address.

export type ProductKey = 'rentals' | 'estates' | 'waste'

/** Origins of the Willcoll apps. An empty string means "not deployed (yet)". */
export interface AppUrls {
  platform: string
  rentals: string
  estates: string
  waste: string
}

export interface ProductInfo { key: ProductKey; label: string; icon: string; blurb: string; available: boolean }

/** Every product Willcoll will have. `available` is false until its app ships. */
export const PRODUCTS: ProductInfo[] = [
  { key: 'rentals', label: 'Rentals', icon: 'lucide:building-2', blurb: 'Rent, water and garbage billing, receipts and the monthly schedule.', available: true },
  { key: 'estates', label: 'Estates', icon: 'lucide:landmark', blurb: 'Service charges and sinking funds for estates and apartments.', available: false },
  { key: 'waste', label: 'Waste', icon: 'lucide:recycle', blurb: 'Collection routes and customer billing for waste services.', available: false },
]

const trimSlash = (s: string) => s.replace(/\/+$/, '')

/** The origin ("https://host:port") of an absolute http(s) URL, else null. */
export function originOf(raw: string): string | null {
  try {
    const u = new URL(raw)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
    if (u.username || u.password) return null
    return u.origin
  } catch {
    return null
  }
}

/** The configured app origins: the allow-list for return_to. */
export function allowedOrigins(apps: Partial<AppUrls>): string[] {
  return Object.values(apps)
    .map(v => (v ? originOf(v) : null))
    .filter((o): o is string => !!o)
}

function hasControlOrBackslash(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c <= 0x1f || c === 0x7f || c === 0x5c) return true
  }
  return false
}

/**
 * Validate a `return_to` value. Returns where to send the browser, or null to
 * ignore it:
 *  - an absolute http(s) URL whose origin is exactly one of the configured app
 *    origins (no userinfo, no lookalike hosts, no other scheme), or
 *  - a path on the current app ("/billing"): one leading slash, never "//host"
 *    or "/\host", which browsers read as another origin.
 */
export function safeReturnTo(raw: unknown, apps: Partial<AppUrls>): string | null {
  if (typeof raw !== 'string') return null
  const value = raw.trim()
  if (!value || value.length > 2048) return null
  // Control characters and backslashes are how a "path" turns into a host.
  if (hasControlOrBackslash(value)) return null
  if (value.startsWith('/')) return value.startsWith('//') ? null : value
  const origin = originOf(value)
  if (!origin || !allowedOrigins(apps).includes(origin)) return null
  return new URL(value).href
}

/** Absolute URL of a path in one of the apps ("" when that app is not configured). */
export function appUrl(apps: Partial<AppUrls>, app: keyof AppUrls, path = '/'): string {
  const base = apps[app]
  if (!base) return ''
  return trimSlash(base) + (path.startsWith('/') ? path : `/${path}`)
}

/** Platform's sign-in page, optionally remembering where to come back to. */
export function loginUrl(apps: Partial<AppUrls>, returnTo?: string | null): string {
  const base = appUrl(apps, 'platform', '/auth/login')
  if (!returnTo) return base
  return `${base}?return_to=${encodeURIComponent(returnTo)}`
}

/** Platform's billing page for one product. */
export function billingUrl(apps: Partial<AppUrls>, product: string): string {
  return appUrl(apps, 'platform', product ? `/billing/${product}` : '/billing')
}

// ---------------------------------------------------------------------------
// Product switcher

/** A manager_modules row as GET /v1/modules returns it. */
export interface ModuleRow {
  product: string
  status: 'requested' | 'trialing' | 'active' | 'suspended' | string
  trial_ends_at?: string | null
}

export interface SwitcherItem { product: ProductKey; label: string; icon: string; url: string; status: string }

/** The signed-in person, as far as the switcher needs. */
export interface SwitcherWho { role?: string; module_access?: string[] }

/** Statuses that mean "this firm uses the product now". */
const LIVE_STATUSES = ['trialing', 'active']

/**
 * The products to offer in the switcher: only modules the firm actually has
 * running (trialing or active; never requested/suspended), only those whose app
 * is deployed, and, for a staff member, only the ones in their module_access.
 * `staff` is null for the firm's own login (the owner), who sees all of them.
 */
export function switcherItems(modules: ModuleRow[], staff: SwitcherWho | null | undefined, apps: Partial<AppUrls>): SwitcherItem[] {
  const out: SwitcherItem[] = []
  for (const p of PRODUCTS) {
    const m = modules.find(x => x.product === p.key)
    if (!m || !LIVE_STATUSES.includes(m.status)) continue
    if (staff && !(staff.module_access ?? []).includes(p.key)) continue
    const url = appUrl(apps, p.key)
    if (!url) continue
    out.push({ product: p.key, label: p.label, icon: p.icon, url, status: m.status })
  }
  return out
}

/** Whole days left in a trial (never negative); null when there is no trial. */
export function trialDaysLeft(trialEndsAt: string | null | undefined, now: Date = new Date()): number | null {
  if (!trialEndsAt) return null
  const end = new Date(trialEndsAt).getTime()
  if (Number.isNaN(end)) return null
  return Math.max(0, Math.ceil((end - now.getTime()) / 86_400_000))
}
