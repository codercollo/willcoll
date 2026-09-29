import { defineStore } from 'pinia'
import { ApiError } from '~/utils/apiError'

export interface Manager {
  id: string
  firm_name: string
  username: string
  email: string
  phone: string
  status: 'pending' | 'active' | 'suspended'
  created_at?: string
}
export interface Admin { id: string; email: string }
export type Principal = 'manager' | 'admin' | null

/** A firm's standing: free trial, paid, or expired (the paywall). */
export interface Access {
  state: 'trial' | 'subscribed' | 'expired'
  days_left: number
  trial_ends_at?: string | null
  subscription_ends_at?: string | null
}

interface SessionBody { principal_type: 'manager' | 'admin'; manager?: Manager; admin?: Admin; access?: Access | null }

export const useAuthStore = defineStore('auth', () => {
  const api = useApi()
  const principal = ref<Principal>(null)
  const manager = ref<Manager | null>(null)
  const admin = ref<Admin | null>(null)
  const access = ref<Access | null>(null)
  const loaded = ref(false) // false until the first GET /sessions answered

  function apply(body: SessionBody | null) {
    principal.value = body?.principal_type ?? null
    manager.value = body?.manager ?? null
    admin.value = body?.admin ?? null
    access.value = body?.access ?? null
  }

  /** Ask the API who is signed in. A 401 is a normal answer here, not an error. */
  async function fetchSession(force = false) {
    if (loaded.value && !force) return
    try {
      apply(await api.get<SessionBody>('/sessions'))
    } catch (e) {
      if (!(e instanceof ApiError) || !e.isAuth) throw e
      apply(null)
    }
    loaded.value = true
  }

  async function login(email: string, password: string) {
    const res = await api.post<{ manager: Manager }>('/sessions', { email, password })
    apply({ principal_type: 'manager', manager: res.manager })
    loaded.value = true
    // The login response does not carry the trial standing; the session read does.
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

  const expired = computed(() => principal.value === 'manager' && access.value?.state === 'expired')

  return { principal, manager, admin, access, expired, markExpired, loaded, fetchSession, login, adminLogin, logout, expire, setManager }
})
