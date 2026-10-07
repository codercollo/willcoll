// Helpers for a public pay page's amount form. M-Pesa takes whole shillings
// only, and a payment may not exceed 250,000 (rentals features section 6).
export const MAX_PAYMENT = 250000

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
