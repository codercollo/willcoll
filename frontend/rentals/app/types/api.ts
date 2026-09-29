// Shapes returned by the API (features-functionalities.txt section 4).
// Money and meter readings are decimal strings; periods are "YYYY-MM".
import type { PageMeta } from '~/components/ui/Pagination.vue'

export type { PageMeta }

export interface Landlord {
  id: string
  name: string
  phone: string
  email?: string | null
  bank_name?: string | null
  bank_account_name?: string | null
  bank_account_number?: string | null
  created_at: string
}

export interface PrintTheme {
  header_text?: string
  address_lines?: string[]
  phone?: string
  accent_color?: string
  reconnection_note?: string
}

export interface PropertySummary {
  period: string
  landlord_name: string
  units_occupied: number
  units_vacant: number
  rent_expected: string
  rent_collected: string
}

export interface Property {
  id: string
  landlord_id: string
  name: string
  location: string
  slug: string
  garbage_enabled: boolean
  garbage_fee: string
  electricity_enabled: boolean
  electricity_deposit_amount: string
  water_rate_per_unit: string
  management_fee_percent: number
  payhero_channel_id?: string | null
  print_theme?: PrintTheme | null
  summary?: PropertySummary
  created_at: string
}

export interface UnitLeaseRef { lease_id: string; tenant_name: string; start_date: string; has_qr?: boolean }

export interface Unit {
  id: string
  property_id: string
  unit_code: string
  meter_number?: string | null
  status: 'vacant' | 'occupied'
  created_at: string
  current_lease?: UnitLeaseRef | null
}

export interface LeasePayer { id: string; name: string; phone?: string | null }

export interface Lease {
  id: string
  unit_id: string
  tenant_name: string
  primary_phone: string
  rent_amount: string
  rent_deposit_amount: string
  water_deposit_amount: string
  electricity_deposit_amount: string
  start_date: string
  end_date?: string | null
  status: 'active' | 'terminated'
  created_at: string
  payers: LeasePayer[]
}

export interface ImportRowError { row: number; field: string; message: string }
