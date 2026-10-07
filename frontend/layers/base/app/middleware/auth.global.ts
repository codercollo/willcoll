import { guardRedirect, platformRedirect, type GuardConfig } from '../utils/routeGuard'
import { safeReturnTo } from '../utils/appLinks'

export default defineNuxtRouteMiddleware(async (to) => {
  const { apps, product } = useRuntimeConfig().public
  const guard = useRuntimeConfig().public.guard as GuardConfig
  // Open paths (the tenant pay page) never wait on, or need, a session.
  if (guard.openPaths.some(p => to.path === p || to.path.startsWith(p + '/'))) return
  const auth = useAuthStore()
  await auth.fetchSession()
  const target = guardRedirect(to.path, auth.principal, auth.access?.state, guard)
  if (!target || target === to.path) return

  // Signed in at a sign-in page that was opened with ?return_to=: go back to
  // the app they came from, but only if it is one of ours (safeReturnTo).
  if (guard.loginMode === 'local' && auth.principal && guard.signInPaths.some(p => to.path.startsWith(p))) {
    const back = safeReturnTo(to.query.return_to, apps)
    if (back) return back.startsWith('/') ? navigateTo(back) : navigateTo(back, { external: true })
  }

  // In a product app the sign-in, Super Admin and billing pages are platform's.
  if (import.meta.client) {
    const away = platformRedirect(target, auth.principal, guard, apps, product, window.location.origin + to.fullPath)
    if (away) return navigateTo(away, { external: true })
  }
  return navigateTo(target)
})
