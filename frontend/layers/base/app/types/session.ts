// Who is signed in, as platform-api reports it (GET /sessions). Plain types, no
// auto-imports, so the sign-in guard and the Nuxt config chain can use them.
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
export interface Staff { id: string; name: string; email: string; role: string; module_access?: string[] }
export type Principal = 'manager' | 'admin' | null

/** A firm's standing: free trial, paid, or expired (the paywall). */
export interface Access {
  state: 'trial' | 'subscribed' | 'expired'
  days_left: number
  trial_ends_at?: string | null
  subscription_ends_at?: string | null
}
