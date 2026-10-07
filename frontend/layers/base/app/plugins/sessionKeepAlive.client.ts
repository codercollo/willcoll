import { createActivityPinger } from '../utils/activityPing'

// Keeps the shared willcoll session alive while the user is active in any
// product app (see utils/activityPing.ts). GET /sessions routes to
// platform-api (utils/apiTarget.ts), the only binary that renews a session.
// A 401 here means the session really ended; useApi then runs its normal
// "session ended, sign in again" handling.
export default defineNuxtPlugin(() => {
  const api = useApi()
  const auth = useAuthStore()

  const pinger = createActivityPinger({
    ping: () => api.get('/sessions'),
    isSignedIn: () => auth.principal !== null,
    isVisible: () => document.visibilityState === 'visible',
  })

  const onActivity = () => { pinger.activity() }
  for (const type of ['pointerdown', 'pointermove', 'keydown', 'scroll', 'wheel', 'touchstart']) {
    window.addEventListener(type, onActivity, { passive: true, capture: true })
  }
  // Coming back to the tab is activity too.
  document.addEventListener('visibilitychange', onActivity)
})
