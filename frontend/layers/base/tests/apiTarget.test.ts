import { describe, expect, it } from 'vitest'
import { isPlatformPath, platformPath } from '~/utils/apiTarget'

// Build-phases.txt 3i-c: auth, account, billing and admin oversight are served by
// platform-api; everything else by rentals-api.
describe('apiTarget', () => {
  it.each([
    '/sessions', '/managers', '/managers/activated', '/managers/password',
    '/tokens/activation', '/tokens/password-reset',
    '/account', '/account/password',
    '/billing/plans', '/billing/subscription', '/billing/subscription/renew', '/billing/invoices', '/billing/invoices/abc/pdf',
    '/staff', '/staff/abc', '/staff/activated', '/staff/me/password', '/modules', '/modules/rentals/trial', '/admin/sessions', '/admin/managers', '/admin/managers/abc/suspend', '/admin/subscriptions', '/admin/plans', '/admin/plans/abc/archive',
    '/admin/system/health', // platform aggregates every service's health
  ])('%s goes to platform-api', (p) => {
    expect(isPlatformPath(p)).toBe(true)
  })

  it.each([
    '/properties', '/properties/abc/billing', '/units/abc', '/landlords', '/payments/review',
    '/pay/runda/A1/otp', '/onboarding', '/leases/abc/qr',
    '/admin/backups', // Rentals' own Super Admin backups page
    '/accounts-receivable', '/sessions-report', // a look-alike prefix is not a match
  ])('%s stays on rentals-api', (p) => {
    expect(isPlatformPath(p)).toBe(false)
  })

  it('matches a path followed by a query string', () => {
    expect(isPlatformPath('/sessions?product=rentals')).toBe(true)
  })

  it('scopes billing to the rentals product', () => {
    expect(platformPath('/billing/plans', 'rentals')).toBe('/billing/rentals/plans')
    expect(platformPath('/billing/subscription/renew', 'rentals')).toBe('/billing/rentals/subscription/renew')
    expect(platformPath('/billing/invoices/42/pdf', 'rentals')).toBe('/billing/rentals/invoices/42/pdf')
  })

  it('leaves a billing path alone when the app has no product (platform names it itself)', () => {
    expect(platformPath('/billing/rentals/plans', '')).toBe('/billing/rentals/plans')
    expect(platformPath('/billing/estates/invoices/42/pdf', '')).toBe('/billing/estates/invoices/42/pdf')
  })

  it('leaves every other platform path alone', () => {
    expect(platformPath('/sessions', 'rentals')).toBe('/sessions')
    expect(platformPath('/admin/managers/1', 'rentals')).toBe('/admin/managers/1')
  })
})
