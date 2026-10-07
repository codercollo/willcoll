import { describe, expect, it } from 'vitest'
import { payLabel } from '~/utils/pay'

describe('pay labels', () => {
  it('labels ledger types', () => {
    expect(payLabel('RENT_DEPOSIT')).toBe('Rent deposit')
    expect(payLabel('garbage')).toBe('Garbage')
  })
})
