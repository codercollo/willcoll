import { defineStore } from 'pinia'
import type { CreateIntentResponse, IntentStatus, PayBalance, PayUnit } from '~/types/pay'

// Tenants never sign in. The OTP-verified pay session is a short-lived Bearer
// token kept in memory, and in sessionStorage (this tab only) so a refresh does
// not make them verify again. Never a cookie.
interface Session { token: string; expiresAt: string; phone: string }

const key = (slug: string, unit: string) => `pay:${slug}:${unit}`

function readStored(slug: string, unit: string): Session | null {
  try {
    const raw = sessionStorage.getItem(key(slug, unit))
    if (!raw) return null
    const s = JSON.parse(raw) as Session
    return new Date(s.expiresAt).getTime() > Date.now() ? s : null
  } catch { return null }
}

export const usePay = defineStore('pay', () => {
  const api = useApi()
  const session = ref<Session | null>(null)
  let slug = ''
  let unit = ''

  function open(s: string, u: string) {
    slug = s; unit = u
    session.value = readStored(s, u)
  }

  function forget() {
    session.value = null
    try { sessionStorage.removeItem(key(slug, unit)) } catch { /* private mode */ }
  }

  const path = (rest = '') => `/pay/${encodeURIComponent(slug)}/${encodeURIComponent(unit)}${rest}`
  const auth = () => ({ token: session.value?.token ?? '' })

  async function info() {
    return (await api.get<{ unit: PayUnit }>(path())).unit
  }

  async function requestCode(phone: string) {
    await api.post(path('/otp'), { phone })
  }

  async function verify(phone: string, code: string) {
    const res = await api.post<{ token: string; expires_at: string }>(path('/otp/verify'), { phone, code })
    session.value = { token: res.token, expiresAt: res.expires_at, phone }
    try { sessionStorage.setItem(key(slug, unit), JSON.stringify(session.value)) } catch { /* private mode */ }
  }

  async function balances() {
    return (await api.get<{ balances: PayBalance[] }>(path('/balances'), undefined, auth())).balances ?? []
  }

  async function createIntent(lines: { type: string; amount: string }[], phone?: string) {
    const body = phone ? { lines, phone } : { lines }
    return (await api.post<CreateIntentResponse>(path('/intent'), body, auth()))
  }

  async function status(intentId: string) {
    return (await api.get<{ intent: IntentStatus }>(`/pay/intents/${intentId}/status`, undefined, auth())).intent
  }

  return { session, open, forget, info, requestCode, verify, balances, createIntent, status }
})
