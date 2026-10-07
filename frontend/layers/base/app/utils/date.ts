const TZ = 'Africa/Nairobi'

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso.length === 10 ? `${iso}T00:00:00+03:00` : iso)
  return new Intl.DateTimeFormat('en-GB', { timeZone: TZ, day: '2-digit', month: 'short', year: 'numeric' }).format(d)
}

export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return ''
  return new Intl.DateTimeFormat('en-GB', { timeZone: TZ, day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(iso))
}

/** dd/mm for a date that is already a Kenya calendar date (report payments). */
export function formatDay(iso: string | null | undefined): string {
  if (!iso) return ''
  return `${iso.slice(8, 10)}/${iso.slice(5, 7)}`
}
