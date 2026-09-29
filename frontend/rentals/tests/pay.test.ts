import { describe, expect, it } from 'vitest'
import { isWholeShillings, looksLikePhone, MAX_PAYMENT, paymentTotal, payLabel, splitProblem } from '~/utils/pay'

describe('pay amounts', () => {
  it('accepts whole shillings only', () => {
    for (const ok of ['1', '3500', '250000', ' 42 ']) expect(isWholeShillings(ok)).toBe(true)
    for (const bad of ['', '0', '01', '3500.50', '1,000', '-5', '1e3', 'abc', '1234567890']) expect(isWholeShillings(bad)).toBe(false)
  })
  it('totals only the valid lines', () => {
    expect(paymentTotal({ RENT: '12000', WATER: '', GARBAGE: 'x' })).toBe(12000)
    expect(paymentTotal({ RENT: '12000', WATER: '2100' })).toBe(14100)
  })
  it('explains why a split cannot be sent', () => {
    expect(splitProblem({})).toMatch(/enter an amount/i)
    expect(splitProblem({ RENT: '', WATER: '' })).toMatch(/enter an amount/i)
    expect(splitProblem({ RENT: '10.5' })).toMatch(/whole shillings/i)
    expect(splitProblem({ RENT: String(MAX_PAYMENT), WATER: '1' })).toMatch(/most you can pay/i)
    expect(splitProblem({ RENT: '12000', WATER: '' })).toBe('')
    expect(splitProblem({ RENT: String(MAX_PAYMENT) })).toBe('')
  })
  it('labels ledger types', () => {
    expect(payLabel('RENT_DEPOSIT')).toBe('Rent deposit')
    expect(payLabel('garbage')).toBe('Garbage')
  })
  it('recognises Kenyan numbers in the spellings people type', () => {
    for (const p of ['0722 111 001', '+254722111001', '254722111001', '0111-222-333']) expect(looksLikePhone(p)).toBe(true)
    for (const p of ['12345', '0622111001', '+255722111001', '']) expect(looksLikePhone(p)).toBe(false)
  })
})
