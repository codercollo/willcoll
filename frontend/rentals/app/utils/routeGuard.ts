import type { Access, Principal } from '~/stores/auth'

// Where should this visitor go? Returns null to allow the navigation.
// Pure so it can be unit tested without a router.
export function guardRedirect(path: string, principal: Principal, access?: Access['state'] | null): string | null {
  const isPublic = path.startsWith('/auth') || path === '/pay' || path.startsWith('/pay/') || path === '/admin/login' || path === '/_design'
  const isAdminArea = path === '/admin' || path.startsWith('/admin/')
  const home = principal === 'admin' ? '/admin/managers' : '/properties'

  if (path === '/') return principal ? home : '/auth/login'
  if (path === '/pay' || path.startsWith('/pay/')) return null // tenants never sign in

  if (!principal) {
    if (isPublic) return null
    return isAdminArea ? '/admin/login' : '/auth/login'
  }
  // Signed in: the sign-in pages are pointless.
  if (path.startsWith('/auth') || path === '/admin/login') return home
  if (isAdminArea && principal !== 'admin') return '/properties'
  // The paywall: a firm whose trial ended and has no plan can only reach the
  // pages needed to pay (and its account).
  if (principal === 'manager' && access === 'expired' && !path.startsWith('/billing') && !path.startsWith('/account')) return '/billing'
  if (!isAdminArea && principal === 'admin' && !isPublic) return '/admin/managers'
  return null
}
