import { canVisit } from '../utils/nav'

// Owner-only pages (staff, modules, billing) bounce a non-owner staff member to
// their products. Runs after auth.global (alphabetical), so the session is loaded.
export default defineNuxtRouteMiddleware((to) => {
  const auth = useAuthStore()
  if (auth.principal !== 'manager') return
  if (!canVisit(to.path, auth.isOwner)) return navigateTo('/')
})
