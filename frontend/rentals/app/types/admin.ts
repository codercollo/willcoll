// Super admin (features-functionalities.txt section 8). The admin never sees a
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
  property_count?: number
  unit_count?: number
}

export interface AdminSubscription {
  id: string
  manager_id: string
  firm_name: string
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

export interface SystemHealth {
  status: string
  database: { status: string; size_bytes: number; connections: number; latency_ms: number; largest_tables: { name: string; size_bytes: number }[] }
  backups: { status: string; last_base_backup: { at?: string; size_bytes?: number } | string | null; wal_archiving: { archived_count: number; failed_count: number; last_archived_wal: string | null; last_failed_at: string | null; last_successful_at: string | null } }
}
