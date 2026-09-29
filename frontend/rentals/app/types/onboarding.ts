// Setup checklist and the onboarding import (backend features section 13).
export interface OnboardingStep {
  key: 'landlord' | 'property' | 'tenants' | 'balances' | 'water' | 'mpesa' | 'billing' | 'stickers'
  title: string
  detail: string
  done: boolean
  required: boolean
}

export interface OnboardingStatus {
  steps: OnboardingStep[]
  complete: boolean
  next: string
  done: number
  total: number
}

/** What an import did, or with a dry run would do. Amounts are decimal strings. */
export interface OnboardSummary {
  units_created: number
  leases_created: number
  vacant_units: number
  monthly_rent: string
  rent_deposits_held: string
  water_deposits_held: string
  rent_arrears: string
  water_arrears: string
  garbage_arrears: string
}
