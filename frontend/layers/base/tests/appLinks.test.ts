import { describe, expect, it } from 'vitest'
import {
  allowedOrigins, appUrl, billingUrl, loginUrl, originOf, safeReturnTo, switcherItems, trialDaysLeft,
  type AppUrls, type ModuleRow,
} from '~/utils/appLinks'
import { platformRedirect, type GuardConfig } from '~/utils/routeGuard'

const dev: AppUrls = { platform: 'http://localhost:3001', rentals: 'http://localhost:3000', estates: '', waste: '' }
const prod: AppUrls = { platform: 'https://app.willcoll.co.ke', rentals: 'https://rentals.willcoll.co.ke/', estates: 'https://estates.willcoll.co.ke', waste: '' }

describe('allowedOrigins', () => {
  it('is the origins of the configured apps, ignoring empty ones and trailing slashes', () => {
    expect(allowedOrigins(dev)).toEqual(['http://localhost:3001', 'http://localhost:3000'])
    expect(allowedOrigins(prod)).toEqual(['https://app.willcoll.co.ke', 'https://rentals.willcoll.co.ke', 'https://estates.willcoll.co.ke'])
  })
  it('originOf refuses non-http schemes and userinfo', () => {
    expect(originOf('javascript:alert(1)')).toBeNull()
    expect(originOf('ftp://localhost:3000')).toBeNull()
    expect(originOf('http://localhost:3000@evil.com')).toBeNull()
    expect(originOf('not a url')).toBeNull()
  })
})

describe('safeReturnTo (no open redirect)', () => {
  it('accepts a page of a configured app', () => {
    expect(safeReturnTo('http://localhost:3000/properties?x=1', dev)).toBe('http://localhost:3000/properties?x=1')
    expect(safeReturnTo('http://localhost:3000', dev)).toBe('http://localhost:3000/')
    expect(safeReturnTo('https://rentals.willcoll.co.ke/payments', prod)).toBe('https://rentals.willcoll.co.ke/payments')
  })
  it('accepts a path on the current app', () => {
    expect(safeReturnTo('/billing/rentals', dev)).toBe('/billing/rentals')
  })
  it('rejects any origin that is not in the allow-list', () => {
    for (const bad of [
      'https://evil.com/',
      'http://evil.com/http://localhost:3000',
      'http://localhost:3002/', // right host, wrong port
      'https://localhost:3000/', // right host and port, wrong scheme
      'http://localhost.evil.com/',
      'https://rentals.willcoll.co.ke.evil.com/',
      'https://evil.com/?next=https://rentals.willcoll.co.ke',
      'http://localhost:3000@evil.com/',
      'http://evil.com\\@localhost:3000/',
      'https://waste.willcoll.co.ke/', // an app that is not configured
    ]) {
      expect(safeReturnTo(bad, prod), bad).toBeNull()
      expect(safeReturnTo(bad, dev), bad).toBeNull()
    }
  })
  it('rejects schemes that run code or leave the web', () => {
    for (const bad of ['javascript:alert(1)', 'data:text/html,<script>1</script>', 'vbscript:x', 'file:///etc/passwd']) {
      expect(safeReturnTo(bad, dev), bad).toBeNull()
    }
  })
  it('rejects protocol-relative and backslash paths that browsers read as another host', () => {
    for (const bad of ['//evil.com', '//evil.com/x', '/\\evil.com', '\\\\evil.com', '/\t/evil.com', '/\n/evil.com']) {
      expect(safeReturnTo(bad, dev), JSON.stringify(bad)).toBeNull()
    }
  })
  it('rejects empty, missing and non-string values', () => {
    for (const bad of [undefined, null, '', '   ', 42, ['http://localhost:3000/'], {}]) {
      expect(safeReturnTo(bad, dev)).toBeNull()
    }
  })
  it('with no apps configured, accepts no absolute URL at all', () => {
    expect(safeReturnTo('http://localhost:3000/', { platform: '', rentals: '', estates: '', waste: '' })).toBeNull()
  })
})

describe('app URLs', () => {
  it('joins an origin and a path without doubling slashes', () => {
    expect(appUrl(prod, 'rentals')).toBe('https://rentals.willcoll.co.ke/')
    expect(appUrl(prod, 'platform', '/account')).toBe('https://app.willcoll.co.ke/account')
    expect(appUrl(prod, 'waste')).toBe('')
  })
  it('builds the sign-in and billing links', () => {
    expect(loginUrl(dev)).toBe('http://localhost:3001/auth/login')
    expect(loginUrl(dev, 'http://localhost:3000/payments?a=1&b=2')).toBe('http://localhost:3001/auth/login?return_to=http%3A%2F%2Flocalhost%3A3000%2Fpayments%3Fa%3D1%26b%3D2')
    expect(billingUrl(dev, 'rentals')).toBe('http://localhost:3001/billing/rentals')
    expect(billingUrl(dev, '')).toBe('http://localhost:3001/billing')
  })
  it('a return_to we built passes our own validation (round trip)', () => {
    const here = 'http://localhost:3000/properties/abc?tab=water'
    const q = new URL(loginUrl(dev, here)).searchParams.get('return_to')
    expect(safeReturnTo(q, dev)).toBe(here)
  })
})

describe('product switcher', () => {
  const m = (product: string, status: string): ModuleRow => ({ product, status })
  const all: AppUrls = { ...prod, waste: 'https://waste.willcoll.co.ke' }

  it('offers the firm its trialing and active products, linked to each app', () => {
    const items = switcherItems([m('rentals', 'active'), m('estates', 'trialing')], null, all)
    expect(items.map(i => [i.product, i.url])).toEqual([
      ['rentals', 'https://rentals.willcoll.co.ke/'],
      ['estates', 'https://estates.willcoll.co.ke/'],
    ])
  })
  it('hides modules that are requested, suspended or never started', () => {
    expect(switcherItems([m('rentals', 'suspended'), m('estates', 'requested')], null, all)).toEqual([])
    expect(switcherItems([], null, all)).toEqual([])
  })
  it('hides a product whose app is not deployed (no URL configured)', () => {
    expect(switcherItems([m('rentals', 'active'), m('waste', 'active')], null, prod).map(i => i.product)).toEqual(['rentals'])
  })
  it('a staff member only sees the products in their module_access', () => {
    const mods = [m('rentals', 'active'), m('estates', 'active'), m('waste', 'trialing')]
    expect(switcherItems(mods, { role: 'officer', module_access: ['estates'] }, all).map(i => i.product)).toEqual(['estates'])
    expect(switcherItems(mods, { role: 'viewer', module_access: ['rentals', 'waste'] }, all).map(i => i.product)).toEqual(['rentals', 'waste'])
  })
  it('a staff member with no module_access sees nothing, and an access entry cannot add a module the firm lacks', () => {
    expect(switcherItems([m('rentals', 'active')], { role: 'viewer', module_access: [] }, all)).toEqual([])
    expect(switcherItems([m('rentals', 'active')], { role: 'viewer' }, all)).toEqual([])
    expect(switcherItems([m('rentals', 'suspended')], { role: 'owner', module_access: ['rentals', 'estates'] }, all)).toEqual([])
  })
  it('the firm\'s own login (no staff row) is not filtered by module_access', () => {
    expect(switcherItems([m('rentals', 'active')], null, all).map(i => i.product)).toEqual(['rentals'])
    expect(switcherItems([m('rentals', 'active')], undefined, all).map(i => i.product)).toEqual(['rentals'])
  })
})

describe('trialDaysLeft', () => {
  const now = new Date('2026-10-07T09:00:00Z')
  it('counts whole days remaining, rounding a partial day up, never below zero', () => {
    expect(trialDaysLeft('2026-10-14T09:00:00Z', now)).toBe(7)
    expect(trialDaysLeft('2026-10-07T20:00:00Z', now)).toBe(1)
    expect(trialDaysLeft('2026-10-01T09:00:00Z', now)).toBe(0)
  })
  it('is null with no trial or an unreadable date', () => {
    expect(trialDaysLeft(null, now)).toBeNull()
    expect(trialDaysLeft(undefined, now)).toBeNull()
    expect(trialDaysLeft('garbage', now)).toBeNull()
  })
})

describe('platformRedirect (a product app sends sign-in, admin and billing to platform)', () => {
  const cfg: GuardConfig = {
    managerHome: '/properties', adminHome: '/admin/managers', loginPath: '/auth/login', adminLoginPath: '/admin/login',
    adminArea: '/admin', signInPaths: [], publicPaths: [], openPaths: ['/pay'], paywallAllowed: ['/billing'], loginMode: 'platform',
  }
  const here = 'http://localhost:3000/payments'

  it('signed-out visitors go to the platform login carrying this page as return_to', () => {
    const url = platformRedirect('/auth/login', null, cfg, dev, 'rentals', here)!
    expect(url.startsWith('http://localhost:3001/auth/login?return_to=')).toBe(true)
    expect(safeReturnTo(new URL(url).searchParams.get('return_to'), dev)).toBe(here)
  })
  it('a Super Admin is sent to platform\'s admin area, a signed-out admin URL to its login', () => {
    expect(platformRedirect('/admin/managers', 'admin', cfg, dev, 'rentals', here)).toBe('http://localhost:3001/admin/managers')
    expect(platformRedirect('/admin/login', null, cfg, dev, 'rentals', here)).toBe('http://localhost:3001/admin/login')
  })
  it('an expired firm is sent to platform\'s billing page for this product', () => {
    expect(platformRedirect('/billing', 'manager', cfg, dev, 'rentals', here)).toBe('http://localhost:3001/billing/rentals')
  })
  it('leaves ordinary pages of this app to the router', () => {
    expect(platformRedirect('/properties', 'manager', cfg, dev, 'rentals', here)).toBeNull()
  })
  it('does nothing in the platform app itself (sign-in pages are local there)', () => {
    expect(platformRedirect('/auth/login', null, { ...cfg, loginMode: 'local' }, dev, '', here)).toBeNull()
  })
})
