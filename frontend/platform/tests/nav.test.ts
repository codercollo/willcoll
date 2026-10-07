import { describe, expect, it } from 'vitest'
import { OWNER_ONLY_PATHS, canVisit, managerNav } from '~/utils/nav'
import { guardRedirect } from '#base/utils/routeGuard'
import { PLATFORM_GUARD } from '../guard.config'

describe('owner-only pages are hidden from staff', () => {
  const labels = (isOwner: boolean) => managerNav(isOwner).map(i => i.label)

  it('an owner (the firm login, or an owner-role staff member) sees staff, modules and billing', () => {
    expect(labels(true)).toEqual(['Products', 'Modules', 'Staff', 'Billing', 'Account'])
  })
  it('anyone else sees only their products and their account', () => {
    expect(labels(false)).toEqual(['Products', 'Account'])
  })
  it('never leaks the internal ownerOnly flag into the nav items', () => {
    for (const i of managerNav(true)) expect('ownerOnly' in i).toBe(false)
  })
  it('a non-owner cannot open staff, modules or billing, nor anything beneath them', () => {
    for (const p of ['/staff', '/staff/abc', '/modules', '/billing', '/billing/rentals', '/billing/rentals/invoices']) {
      expect(canVisit(p, false), p).toBe(false)
      expect(canVisit(p, true), p).toBe(true)
    }
  })
  it('a non-owner can open everything else, and a look-alike prefix is not owner-only', () => {
    for (const p of ['/', '/account', '/staffing', '/billing-help', '/modulesx']) expect(canVisit(p, false), p).toBe(true)
  })
  it('every owner-only path is also in the owner nav, so the two lists cannot drift', () => {
    const to = managerNav(true).map(i => i.to)
    for (const p of OWNER_ONLY_PATHS) expect(to).toContain(p)
  })
})

describe('platform sign-in guard', () => {
  const g = (path: string, principal: 'manager' | 'admin' | null) => guardRedirect(path, principal, null, PLATFORM_GUARD)

  it('signed-out visitors go to login, but every /auth page and the admin login stay open', () => {
    expect(g('/', null)).toBe('/auth/login')
    expect(g('/account', null)).toBe('/auth/login')
    expect(g('/staff', null)).toBe('/auth/login')
    expect(g('/admin/managers', null)).toBe('/admin/login')
    for (const p of ['/auth/login', '/auth/signup', '/auth/activate', '/auth/activate-staff', '/auth/forgot-password', '/auth/reset-password', '/admin/login']) {
      expect(g(p, null), p).toBeNull()
    }
  })
  it('a signed-in firm lands on its product cards at "/"', () => {
    expect(g('/', 'manager')).toBe('/')
    expect(g('/auth/login', 'manager')).toBe('/')
    expect(g('/account', 'manager')).toBeNull()
  })
  it('a Super Admin lives under /admin', () => {
    expect(g('/', 'admin')).toBe('/admin/managers')
    expect(g('/account', 'admin')).toBe('/admin/managers')
    expect(g('/admin/plans', 'admin')).toBeNull()
    expect(g('/admin/managers', 'manager')).toBe('/')
  })
})
