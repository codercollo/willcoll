import { guardRedirect } from '~/utils/routeGuard'

export default defineNuxtRouteMiddleware(async (to) => {
  if (to.path === '/pay' || to.path.startsWith('/pay/')) return
  const auth = useAuthStore()
  await auth.fetchSession()
  const target = guardRedirect(to.path, auth.principal, auth.access?.state)
  if (target && target !== to.path) return navigateTo(target)
})
