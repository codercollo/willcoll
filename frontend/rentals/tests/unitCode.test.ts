import { describe, expect, it } from 'vitest'
import { formatUnitCodeInput, isUnitCode, normalizeUnitCode } from '~/utils/unitCode'
import { guardRedirect as guard, platformRedirect } from '#base/utils/routeGuard'
import { RENTALS_GUARD } from '../guard.config'

const guardRedirect = (path: string, principal: 'manager' | 'admin' | null) => guard(path, principal, null, RENTALS_GUARD)

describe('unit code entry', () => {
  it('ignores case, dashes and spaces', () => {
    expect(normalizeUnitCode('k7qm-2xh9 ptrb')).toBe('K7QM2XH9PTRB')
    expect(normalizeUnitCode(' K7QM_2XH9_PTRB ')).toBe('K7QM2XH9PTRB')
  })
  it('accepts only 12 characters from the unambiguous alphabet', () => {
    expect(isUnitCode('K7QM-2XH9-PTRB')).toBe(true)
    expect(isUnitCode('k7qm2xh9ptrb')).toBe(true)
    for (const bad of ['', 'K7QM-2XH9', 'K7QM-2XH9-PTRB-X', 'K7QM-2XH9-PTR0', 'K7QM-2XH9-PTRO', 'K7QM-2XH9-PTRI', 'K7QM-2XH9-PTRL', 'K7QM-2XH9-PTR!']) {
      expect(isUnitCode(bad)).toBe(false)
    }
  })
  it('formats as you type', () => {
    expect(formatUnitCodeInput('k7qm')).toBe('K7QM')
    expect(formatUnitCodeInput('k7qm2')).toBe('K7QM-2')
    expect(formatUnitCodeInput('k7qm2xh9ptrbEXTRA')).toBe('K7QM-2XH9-PTRB')
    expect(formatUnitCodeInput('K7QM-2XH9-PTRB')).toBe('K7QM-2XH9-PTRB')
    expect(formatUnitCodeInput('')).toBe('')
  })
})

describe('Rentals hands sign-in, admin and billing to the platform app (3l)', () => {
  const apps = { platform: 'http://localhost:3001', rentals: 'http://localhost:3000', estates: '', waste: '' }
  const here = 'http://localhost:3000/payments'
  const go = (path: string, principal: 'manager' | 'admin' | null, access: 'expired' | null = null) => {
    const target = guard(path, principal, access, RENTALS_GUARD)
    return target ? platformRedirect(target, principal, RENTALS_GUARD, apps, 'rentals', here) : null
  }

  it('declares that the sign-in pages live in platform', () => {
    expect(RENTALS_GUARD.loginMode).toBe('platform')
    expect(RENTALS_GUARD.signInPaths).toEqual([])
  })
  it('an unauthenticated visit goes to the platform login with return_to set to the page', () => {
    const url = new URL(go('/payments', null)!)
    expect(url.origin + url.pathname).toBe('http://localhost:3001/auth/login')
    expect(url.searchParams.get('return_to')).toBe(here)
  })
  it('the QR print page needs a login, the public pay page does not', () => {
    expect(go('/print/qr/abc', null)).not.toBeNull()
    expect(go('/pay/runda-arcade/G1', null)).toBeNull()
  })
  it('a Super Admin is sent to platform\'s admin area', () => {
    expect(go('/properties', 'admin')).toBe('http://localhost:3001/admin/managers')
  })
  it('an expired firm is sent to platform billing for rentals; a live one is left alone', () => {
    expect(go('/properties', 'manager', 'expired')).toBe('http://localhost:3001/billing/rentals')
    expect(go('/properties', 'manager')).toBeNull()
  })
})

describe('the pay landing and inactive pages need no login', () => {
  it('allows /pay and /pay/inactive for anyone', () => {
    for (const p of ['/pay', '/pay/inactive', '/pay/runda-arcade/G1']) {
      expect(guardRedirect(p, null)).toBeNull()
      expect(guardRedirect(p, 'manager')).toBeNull()
    }
  })
  it('does not treat /payments as a pay page', () => {
    expect(guardRedirect('/payments', null)).toBe('/auth/login')
  })
})
