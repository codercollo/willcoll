// Billing engine and ledger shapes (features-functionalities.txt section 5).
// Every amount and reading is a decimal string. Never do arithmetic on them.
import type { PageMeta } from '~/types/api'

export type LedgerType = 'rent' | 'water' | 'garbage' | 'rent_deposit' | 'water_deposit' | 'electricity_deposit'
export type RentStatus = 'paid' | 'partial' | 'arrears'

export interface RentOverviewRow {
  unit_id: string
  unit_code: string
  tenant_name: string
  expected: string
  billed: boolean
  paid_this_period: string
  balance: string // negative = advance (credit)
  status: RentStatus
}

export interface RentRun { period: string; billed: number; skipped: number; total_amount: string }

export interface WaterRow {
  unit_id: string
  unit_code: string
  tenant_name: string
  has_reading: boolean
  previous_reading: string
  current_reading: string
  units_consumed: string // computed by the database
  rate: string
  amount: string // computed by the database
  prior_balance: string
  total_due: string
  locked: boolean
}

export interface WaterGrid { period: string; default_rate: string; rows: WaterRow[] }
export interface WaterReadingInput { unit_id: string; current_reading: string; previous_reading?: string; rate?: string }
export interface WaterRun { billed: number; total: string; missing: string[] }

export interface GarbageRow { unit_id: string; unit_code: string; tenant_name: string; billed: boolean; fee: string; prior_balance: string; total_due: string }
export interface GarbagePreview { period: string; enabled: boolean; fee: string; already_generated: boolean; billed_count: number; message: string; rows: GarbageRow[] }

export interface ElectricityDepositRow { unit_id: string; unit_code: string; tenant_name: string; deposit_required: string; paid: string; balance: string }
export interface ElectricityDeposits {
  property_name: string
  enabled: boolean
  rows: ElectricityDepositRow[]
  total_required: string
  total_paid: string
  total_balance: string
}

export interface LedgerEntry {
  id: string
  direction: 'DEBIT' | 'CREDIT'
  amount: string
  reference_type: string
  created_at: string
  transaction_type: string
  description: string
  running_balance: string
  reversed: boolean
}

export interface LedgerPage {
  balance: { unit_id: string; type: string; balance: string }
  entries: LedgerEntry[]
  metadata: Partial<PageMeta>
}

export interface PaymentInput { amount: string; source: 'manual' | 'bank'; reference?: string; note?: string; ledger_type?: LedgerType }
export interface ChargeInput { amount: string; description: string; ledger_type?: LedgerType }
