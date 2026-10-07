// Session keep-alive (build-phases.txt, "Sliding sessions"). platform-api
// slides a session's idle window only when it sees a request, and only
// platform-api writes the session, so a user working in Rentals for an hour
// would otherwise look idle to it. This throttles a cheap GET /v1/sessions
// to platform-api while the user is really using ANY app.
//
// Pure and clock-injected so the debounce is unit-testable; the browser
// wiring (events, visibility, the request) is plugins/sessionKeepAlive.client.ts.

/** At most one ping per this long. platform-api itself renews at most every idle/4 (30 min by default). */
export const PING_INTERVAL_MS = 4 * 60 * 1000

export interface ActivityPingerOptions {
  /** Sends the request. Failures are the caller's business; the pinger never throws. */
  ping: () => Promise<unknown> | unknown
  /** Signed in right now? A signed-out user is never pinged. */
  isSignedIn: () => boolean
  /** False while the tab is hidden. */
  isVisible: () => boolean
  intervalMs?: number
  now?: () => number
}

export interface ActivityPinger {
  /** Call on every user-activity event; cheap when nothing is due. Returns whether it pinged. */
  activity: () => boolean
}

export function createActivityPinger(opts: ActivityPingerOptions): ActivityPinger {
  const interval = opts.intervalMs ?? PING_INTERVAL_MS
  const now = opts.now ?? Date.now
  let last = -Infinity
  let inFlight = false

  function activity(): boolean {
    if (inFlight || !opts.isVisible() || !opts.isSignedIn()) return false
    const t = now()
    if (t - last < interval) return false
    // Claim the slot before the request settles, so a burst of events (and a
    // failing request) can't turn into a burst of pings.
    last = t
    inFlight = true
    Promise.resolve()
      .then(() => opts.ping())
      .catch(() => { /* a failed keep-alive is retried on the next window */ })
      .finally(() => { inFlight = false })
    return true
  }

  return { activity }
}
