// Which API serves a path (build-phases.txt 3i-c). Identity, account, billing
// and the Super Admin's manager/subscription/plan oversight live on
// platform-api; everything else (the portfolio, ledgers, the pay page, the
// Rentals backups page) stays on the product's own API. useApi routes by this one list,
// so a screen never has to know which binary it is talking to.

const PLATFORM_PREFIXES = [
  '/sessions',
  '/managers',
  '/tokens',
  '/account',
  '/billing',
  '/staff',
  '/modules',
  '/admin/sessions',
  '/admin/managers',
  '/admin/subscriptions',
  '/admin/plans',
  '/admin/system', // the one system health page: platform checks each service itself
]

/** True when `path` (without the /v1 prefix) is served by platform-api. */
export function isPlatformPath(path: string): boolean {
  return PLATFORM_PREFIXES.some(
    (p) => path === p || path.startsWith(`${p}/`) || path.startsWith(`${p}?`),
  )
}

/**
 * The path platform-api actually serves. Billing is per product there
 * (/billing/:product/...), where the old Rentals API had one subscription and
 * no product segment, so /billing/plans becomes /billing/<product>/plans.
 * An app with no product of its own (platform) names the product itself:
 * its paths are already /billing/<product>/..., and pass through unchanged.
 */
export function platformPath(path: string, product: string): string {
  if (!product) return path
  if (path === '/billing' || path.startsWith('/billing/') || path.startsWith('/billing?')) {
    return `/billing/${product}${path.slice('/billing'.length)}`
  }
  return path
}
