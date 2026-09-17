import { usePermissions, type SessionClaims } from '~/composables/usePermissions'

interface AuthResponse {
  token: string
  user_id: string
  role: SessionClaims['role']
  organization_id: string
}

const STORAGE_KEY = 'auth-token'
const CLAIMS_KEY = 'auth-claims'

export function useAuth() {
  const token = useState<string | null>('auth-token', () => {
    if (import.meta.client) return localStorage.getItem(STORAGE_KEY)
    return null
  })
  const { setClaims } = usePermissions()

  function persist(auth: AuthResponse) {
    token.value = auth.token
    const claims: SessionClaims = { user_id: auth.user_id, role: auth.role, organization_id: auth.organization_id }
    setClaims(claims)
    if (import.meta.client) {
      localStorage.setItem(STORAGE_KEY, auth.token)
      localStorage.setItem(CLAIMS_KEY, JSON.stringify(claims))
    }
  }

  // PASETO v2.local is symmetrically encrypted — it can never be decoded in
  // the browser. Claims come only from an authResponse the server issued
  // (login/register/refresh), never guessed from the token itself.
  function restoreClaims() {
    if (!import.meta.client) return
    const raw = localStorage.getItem(CLAIMS_KEY)
    if (raw) setClaims(JSON.parse(raw))
  }

  async function login(email: string, password: string) {
    const response = await $fetch<{ data: AuthResponse }>('/v1/auth/login', { method: 'POST', body: { email, password } })
    persist(response.data)
    return response.data.token
  }

  async function refresh() {
    const response = await $fetch<{ data: AuthResponse }>('/v1/auth/refresh', {
      method: 'POST',
      headers: token.value ? { Authorization: `Bearer ${token.value}` } : {},
    })
    persist(response.data)
    return response.data.token
  }

  async function activate(activationToken: string, password: string) {
    await $fetch('/v1/users/activate', { method: 'PUT', body: { token: activationToken, password } })
  }

  async function resetPassword(resetToken: string, newPassword: string) {
    await $fetch('/v1/users/password', { method: 'PUT', body: { token: resetToken, new_password: newPassword } })
  }

  function logout() {
    token.value = null
    setClaims(null)
    if (import.meta.client) {
      localStorage.removeItem(STORAGE_KEY)
      localStorage.removeItem(CLAIMS_KEY)
    }
  }

  return { token, login, refresh, activate, resetPassword, logout, restoreClaims }
}
