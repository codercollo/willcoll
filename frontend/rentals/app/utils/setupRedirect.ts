// A firm with no properties is sent to the Rentals setup guide once, the first
// time it lands on the portfolio after signing in (build-phases.txt Phase 3
// close-out; it used to happen in Rentals' own login page, which moved to
// platform in 3l). "Once" is per firm per browser tab: the flag holds the id of
// the firm already sent, so a later visit to an empty portfolio (someone
// deliberately looking at it) is never bounced, and a different firm signing in
// on the same tab still gets its redirect.

const KEY = 'willcoll:setup-redirected'

/** Pure decision, so it can be tested without a router or storage. */
export function shouldSendToSetup(propertyCount: number, firmId: string | undefined, alreadySentFirmId: string | null): boolean {
  if (!firmId || propertyCount > 0) return false
  return alreadySentFirmId !== firmId
}

// Storage can be missing or throw (private windows, blocked site data): then we
// behave as if nothing was recorded, which at worst repeats a redirect.
export function readSetupRedirected(): string | null {
  try { return sessionStorage.getItem(KEY) } catch { return null }
}

export function markSetupRedirected(firmId: string): void {
  try { sessionStorage.setItem(KEY, firmId) } catch { /* nothing to remember it in */ }
}
