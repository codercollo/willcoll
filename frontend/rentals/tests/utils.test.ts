import { describe, expect, it } from 'vitest'
import { formatMoney, isNegativeMoney, isValidMoneyInput, isZeroMoney } from '~/utils/money'
import { currentPeriod, periodLabel, shiftPeriod } from '~/utils/period'
import { parseApiError } from '~/utils/apiError'

describe('money', () => {
  it('formats strings without floats', () => {
    expect(formatMoney('1900.5')).toBe('1,900.50')
    expect(formatMoney('18000.00')).toBe('18,000.00')
    expect(formatMoney('9999999999.99')).toBe('9,999,999,999.99')
    expect(formatMoney('-250')).toBe('-250.00')
    expect(formatMoney(null)).toBe('0.00')
  })
  it('classifies', () => {
    expect(isZeroMoney('0.00')).toBe(true)
    expect(isNegativeMoney('-1.00')).toBe(true)
    expect(isNegativeMoney('-0.00')).toBe(false)
  })
  it('validates input like the API', () => {
    expect(isValidMoneyInput('1000.5')).toBe(true)
    expect(isValidMoneyInput('1,000')).toBe(false)
    expect(isValidMoneyInput('1.005')).toBe(false)
    expect(isValidMoneyInput('1e3')).toBe(false)
  })
})

describe('period', () => {
  it('shifts across years', () => {
    expect(shiftPeriod('2026-01', -1)).toBe('2025-12')
    expect(shiftPeriod('2026-11', 3)).toBe('2027-02')
    expect(periodLabel('2026-09')).toBe('September 2026')
  })
  it('uses Kenya calendar time', () => {
    expect(currentPeriod(new Date('2026-08-31T21:30:00Z'))).toBe('2026-09')
  })
})

describe('ApiError', () => {
  it('reads a string error', () => expect(parseApiError(409, { error: 'edit conflict' }).message).toBe('edit conflict'))
  it('maps 422 objects to fields', () => {
    const e = parseApiError(422, { error: { price: 'must be positive' } })
    expect(e.fields.price).toBe('must be positive')
    expect(e.isValidation).toBe(true)
  })
  it('falls back to a default', () => expect(parseApiError(500, null).message).toMatch(/our side/))
})
