// Monthly report (ALL IN ONE PAYMENTS SCHEDULE preview), features section 5.
export interface ReportPayment { date: string; amount: string }

export interface ReportRow {
  house_no: string
  tenants: string[] | null // tenant first, then co-payers ("OR" names)
  rent: ReportPayment[] | null
  water: ReportPayment[] | null
  garbage: ReportPayment[] | null
  rent_deposit: ReportPayment[] | null
  water_deposit: ReportPayment[] | null
  electricity_deposit: ReportPayment[] | null
}

export interface Report {
  period: string
  property_name: string
  location: string
  garbage_enabled: boolean
  electricity_deposit_shown: boolean
  summary: { occupied: number; vacant: number; water_units: string; water_rate: string; expected_water: string; actual_water: string }
  deviation: string
  rows: ReportRow[]
  totals: { rent: string; water: string; garbage: string; rent_deposit: string; water_deposit: string; electricity_deposit: string; grand_total: string }
  note1: string[] | null
  notes: string[]
  management_fee_percent: number
  management_fee: string
  confirmed: boolean
  confirmed_at?: string
  stale: boolean
}

export interface SanityFailure { code: string; message: string }
export interface SanityChecks { failures: SanityFailure[] | null; info: string[] | null }
export interface ReportChecks { checks: SanityChecks; report: Report }

export interface PlotMeterReading {
  previous_reading: string | null
  current_reading: string | null
  units_consumed: string | null
  reading_date: string | null
}
