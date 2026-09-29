// Helpers for the tenant pay page. M-Pesa takes whole shillings only, and a
// payment may not exceed 250,000 (features section 6).
export const MAX_PAYMENT = 250000

const LABELS: Record<string, string> = {
  RENT: 'Rent', WATER: 'Water', GARBAGE: 'Garbage', RENT_DEPOSIT: 'Rent deposit', WATER_DEPOSIT: 'Water deposit',
  ELECTRICITY_DEPOSIT: 'Electricity deposit',
}
export const payLabel = (type: string) => LABELS[type.toUpperCase()] ?? type

// A one-off refundable deposit can't be paid ahead (system-design.txt
// 3.2.1): the input is capped at what's outstanding and disabled once it's
// fully paid. Rent/water/garbage may still be overpaid (an advance).
const DEPOSIT_TYPES = new Set(['RENT_DEPOSIT', 'WATER_DEPOSIT', 'ELECTRICITY_DEPOSIT'])
export const isDepositType = (type: string) => DEPOSIT_TYPES.has(type.toUpperCase())

/** A positive whole number of shillings, no separators or decimals. */
export function isWholeShillings(v: string): boolean {
  return /^[1-9]\d{0,8}$/.test(v.trim())
}

export function paymentTotal(amounts: Record<string, string>): number {
  return Object.values(amounts).reduce((a, v) => a + (isWholeShillings(v) ? Number(v) : 0), 0)
}

/** Why the split cannot be submitted, or '' when it can. */
export function splitProblem(amounts: Record<string, string>): string {
  const entered = Object.entries(amounts).filter(([, v]) => v.trim() !== '')
  if (!entered.length) return 'Enter an amount to pay.'
  if (entered.some(([, v]) => !isWholeShillings(v))) return 'Use whole shillings only, for example 3500.'
  const total = paymentTotal(amounts)
  if (total > MAX_PAYMENT) return `The most you can pay at once is Ksh ${MAX_PAYMENT.toLocaleString('en-KE')}.`
  return ''
}

/** Normalise a Kenyan number for display/entry checks (the API also accepts local spellings). */
export function looksLikePhone(v: string): boolean {
  return /^(\+?254|0)[17]\d{8}$/.test(v.replace(/[\s-]/g, ''))
}
