// Super admin (platform-api /v1/admin/*). The admin never sees a
// manager's ledger data: only firm, subscription and counts.
export interface AdminManager {
  id: string
  firm_name: string
  username: string
  email: string
  phone: string
  status: 'pending' | 'active' | 'suspended'
  activated_at?: string | null
  created_at: string
  subscription?: { status: string; current_period_end?: string } | null
  // property_count is gone: properties live in the Rentals database, which platform-api never reads.
  staff_count?: number
  unit_count?: number
  // Detail (GET /admin/managers/:id): every product the firm holds.
  modules?: AdminModule[]
  subscriptions?: AdminProductSubscription[]
}

export interface AdminModule { product: string; status: string; trial_ends_at?: string | null; activated_at?: string | null }
export interface AdminProductSubscription { product: string; status: string; plan_name: string; current_period_end: string }

export interface AdminSubscription {
  id: string
  manager_id: string
  firm_name: string
  product?: string
  plan_id: string
  plan_name: string
  plan_price: string
  billing_interval: string
  status: 'trialing' | 'active' | 'past_due' | 'cancelled'
  current_period_start: string
  current_period_end: string
}

export interface AdminPlan {
  id: string
  name: string
  price: string
  billing_interval: 'weekly' | 'monthly' | 'quarterly' | 'yearly'
  unit_cap?: number | null
  subscribers: number
  is_test: boolean
  archived: boolean
  pricing_type: 'flat' | 'per_unit'
  per_unit_price?: string | null
  min_price?: string | null
  sort_order: number
}
export interface PlanInput {
  name?: string
  price?: string
  billing_interval?: string
  unit_cap?: number | null
  is_test?: boolean
  pricing_type?: 'flat' | 'per_unit'
  per_unit_price?: string | null
  min_price?: string | null
  sort_order?: number
}

export interface DatabaseHealth {
  status: string
  error?: string
  size_bytes?: number
  connections?: number
  latency_ms?: number
  largest_tables?: { name: string; size_bytes: number }[]
}
export interface WalArchiving { archived_count: number; failed_count: number; last_archived_wal: string | null; last_failed_at: string | null; last_successful_at: string | null }

/** A PayHero channel a payment arrived on that has no claim in platform (still posted locally). */
export interface UnclaimedChannel { channel_id: string; property_id?: string; property_name?: string; count: number; last_seen_at: string }

/** rentals-api's own report, passed through by platform-api. */
export interface RentalsReport {
  status: string
  database: DatabaseHealth
  backups: { status: string; last_base_backup: { completed_at?: string; size_bytes?: number } | null; wal_archiving: WalArchiving }
  usage_reporting: { enabled: boolean; last_report_at: string | null }
  audit_failures_24h: number
  inbox_fallbacks_24h: number
  unclaimed_channels?: UnclaimedChannel[]
  /** Firms that hit their daily pay-OTP SMS ceiling in the last 24h. */
  otp_ceiling_firms?: { tenant_id: string; last_hit_at: string }[]
}

/** ok | degraded | unreachable | unauthorized | not_configured: one service, whatever the others did. */
export type ServiceStatus = 'ok' | 'degraded' | 'unreachable' | 'unauthorized' | 'not_configured'

export interface SystemHealth {
  status: 'available' | 'degraded' | 'unavailable'
  checked_at: string
  system_info: { environment: string; version: string }
  services: {
    platform: { status: ServiceStatus; database: DatabaseHealth; wal_archiving?: WalArchiving; audit_failures_24h?: number }
    rentals: { status: ServiceStatus; latency_ms?: number; error?: string; report?: RentalsReport }
  }
}
