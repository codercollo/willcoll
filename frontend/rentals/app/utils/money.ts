// Money is a decimal STRING end to end (features 1.4). Never go through a
// float: format by splitting the string.
export function formatMoney(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '') return '0.00'
  const m = /^(-?)(\d+)(?:\.(\d{1,2}))?$/.exec(String(value).trim())
  if (!m) return String(value)
  const sign = m[1] ?? ''
  const whole = m[2] ?? '0'
  const frac = m[3] ?? ''
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${sign}${grouped}.${frac.padEnd(2, '0')}`
}

export function isZeroMoney(value: string | null | undefined): boolean {
  return !value || /^-?0+(\.0+)?$/.test(String(value).trim())
}

export function isNegativeMoney(value: string | null | undefined): boolean {
  return !!value && /^-/.test(String(value).trim()) && !isZeroMoney(value)
}

// A money string is valid input if the API will accept it (2 places max).
export function isValidMoneyInput(value: string): boolean {
  return /^\d{1,10}(\.\d{1,2})?$/.test(value.trim())
}

export function toCents(v: string | null | undefined): number {
  const m = /^(-?)(\d+)(?:\.(\d{1,2}))?$/.exec(String(v ?? '0').trim())
  if (!m) return 0
  const cents = Number(m[2]) * 100 + Number((m[3] ?? '').padEnd(2, '0') || 0)
  return m[1] ? -cents : cents
}

/** part / whole as 0..1, for progress bars only (never for a displayed amount). */
export function moneyRatio(part: string | null | undefined, whole: string | null | undefined): number {
  const w = toCents(whole)
  if (w <= 0) return 0
  return Math.min(1, Math.max(0, toCents(part) / w))
}

/** Exact sum of money strings via integer cents (display totals only). */
export function sumMoney(values: (string | null | undefined)[]): string {
  const cents = values.reduce<number>((a, v) => a + toCents(v), 0)
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(cents)
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`
}

export function fromCents(cents: number): string {
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(cents)
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, '0')}`
}

/** a - b, exactly (integer cents), as a money string. */
export function subMoney(a: string | null | undefined, b: string | null | undefined): string {
  return fromCents(toCents(a) - toCents(b))
}

/** Compare two money strings: negative if a < b, 0 if equal, positive if a > b. */
export function cmpMoney(a: string | null | undefined, b: string | null | undefined): number {
  return toCents(a) - toCents(b)
}
