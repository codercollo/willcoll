import { describe, expect, it } from 'vitest'
import { formatUnitCodeInput, isUnitCode, normalizeUnitCode } from '~/utils/unitCode'
import { guardRedirect } from '~/utils/routeGuard'

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
