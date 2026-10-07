import { defineStore } from 'pinia'
import { ApiError } from '../utils/apiError'
import type { Access, Admin, Manager, Principal, Staff } from '../types/session'
import type { ModuleRow } from '../utils/appLinks'

export type { Access, Admin, Manager, Principal, Staff }


// A staff member signs in at the same endpoint as their firm's manager
// (platform-api): the session says principal_type 'staff' and carries the
// firm as `manager`.
interface SessionBody {
  principal_type: 'manager' | 'staff' | 'admin'
  manager?: Manager
  admin?: Admin
  staff?: Staff
  access?: Access | null
}

export const useAuthStore = defineStore('auth', () => {
  const api = useApi()
  const principal = ref<Principal>(null)
  const manager = ref<Manager | null>(null)
  const admin = ref<Admin | null>(null)
  const staff = ref<Staff | null>(null)
  const access = ref<Access | null>(null)
  const loaded = ref(false) // false until the first GET /sessions answered
  // The firm's products (GET /modules; a staff member only gets their own).
  // null until asked: the product switcher and platform's home page read it.
  const modules = ref<ModuleRow[] | null>(null)

  function apply(body: SessionBody | null) {
    // Staff use the firm's dashboard, so the shell treats them as the manager side.
    principal.value = body ? (body.principal_type === 'admin' ? 'admin' : 'manager') : null
    manager.value = body?.manager ?? null
    admin.value = body?.admin ?? null
    staff.value = body?.staff ?? null
    access.value = body?.access ?? null
    if (!body) modules.value = null // belonged to whoever was signed in
  }

  /** Ask platform-api who is signed in. A 401 is a normal answer here, not an error. */
  async function fetchSession(force = false) {
    if (loaded.value && !force) return
    try {
      // ?product= adds the firm's trial/paywall standing in Rentals.
      apply(await api.get<SessionBody>('/sessions', { product: useRuntimeConfig().public.product }))
    } catch (e) {
      if (!(e instanceof ApiError) || !e.isAuth) throw e
      apply(null)
    }
    loaded.value = true
  }

  /** Load the firm's modules (once, or again when `force`). A failure leaves the list empty. */
  async function fetchModules(force = false) {
    if (modules.value && !force) return
    if (!principal.value || principal.value === 'admin') { modules.value = []; return }
    try {
      modules.value = (await api.get<{ modules: ModuleRow[] }>('/modules')).modules ?? []
    } catch {
      modules.value = []
    }
  }

  async function login(email: string, password: string) {
    // One endpoint for the manager and their staff. The login response does
    // not carry the trial standing; the session read does.
    const res = await api.post<{ manager?: Manager; staff?: Staff }>('/sessions', { email, password })
    apply({ principal_type: res.staff ? 'staff' : 'manager', manager: res.manager, staff: res.staff })
    loaded.value = true
    try { await fetchSession(true) } catch { /* the sign-in itself succeeded */ }
  }

  async function adminLogin(email: string, password: string) {
    const res = await api.post<{ admin: Admin }>('/admin/sessions', { email, password })
    apply({ principal_type: 'admin', admin: res.admin })
    loaded.value = true
  }

  async function logout() {
    try { await api.del('/sessions') } catch { /* already signed out */ }
    apply(null)
  }

  /** Called when any request comes back 401: the session is gone. */
  function expire() { apply(null); loaded.value = true }

  function setManager(m: Manager) { manager.value = m }

  /** The API answered 402: the trial is over and no plan is active. */
  function markExpired() { access.value = { state: 'expired', days_left: 0 } }

  /** The firm's own login, or a staff member whose role is owner (system-design A.1). */
  const isOwner = computed(() => principal.value === 'manager' && (!staff.value || staff.value.role === 'owner'))

  const expired = computed(() => principal.value === 'manager' && access.value?.state === 'expired')

  return { principal, manager, admin, staff, access, modules, isOwner, fetchModules, expired, markExpired, loaded, fetchSession, login, adminLogin, logout, expire, setManager }
})
