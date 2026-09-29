// Periods are "YYYY-MM" (features 1.4).
const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December']

export function currentPeriod(now: Date = new Date()): string {
  const p = nairobiParts(now)
  return `${p.year}-${String(p.month).padStart(2, '0')}`
}

export function shiftPeriod(period: string, months: number): string {
  const [y, m] = period.split('-').map(Number) as [number, number]
  const idx = y * 12 + (m - 1) + months
  return `${Math.floor(idx / 12)}-${String((idx % 12) + 1).padStart(2, '0')}`
}

export function periodLabel(period: string): string {
  const [y, m] = period.split('-').map(Number) as [number, number]
  return `${MONTHS[m - 1]} ${y}`
}

export function isPeriod(value: string): boolean {
  return /^\d{4}-(0[1-9]|1[0-2])$/.test(value)
}

// Calendar parts in Kenya time: a payment at 00:30 EAT on the 1st is the new month.
export function nairobiParts(d: Date): { year: number; month: number; day: number } {
  const f = new Intl.DateTimeFormat('en-CA', { timeZone: 'Africa/Nairobi', year: 'numeric', month: '2-digit', day: '2-digit' })
  const [year, month, day] = f.format(d).split('-').map(Number) as [number, number, number]
  return { year, month, day }
}
