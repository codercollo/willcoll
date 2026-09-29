// The spreadsheet a firm fills in to bring a building in. One row per unit; a
// row with no tenant_name is a vacant unit. Only unit_code is required, but a
// row with a tenant needs phone and rent.

export interface OnboardColumn { key: string; label: string; required: string; help: string }

export const ONBOARD_COLUMNS: OnboardColumn[] = [
  { key: 'unit_code', label: 'Unit code', required: 'Always', help: 'The house number as on the door, e.g. G1 or SHOP NO.2.' },
  { key: 'meter_number', label: 'Meter number', required: 'No', help: "The unit's water meter. Used when the unit is new." },
  { key: 'tenant_name', label: 'Tenant name', required: 'For a tenant', help: 'Leave the whole row empty (except unit_code) for a vacant unit.' },
  { key: 'phone', label: 'Phone', required: 'For a tenant', help: 'The number they pay from: 0722 000 000 or +254722000000.' },
  { key: 'rent', label: 'Monthly rent', required: 'For a tenant', help: 'e.g. 18000 or Ksh 18,000.' },
  { key: 'start_date', label: 'Lease start', required: 'No', help: 'e.g. 2025-07-01 or 01/07/2025. Defaults to the as-at date.' },
  { key: 'rent_deposit', label: 'Rent deposit held', required: 'No', help: 'Deposit the tenant has ALREADY paid. It is recorded as held, not as a payment.' },
  { key: 'water_deposit', label: 'Water deposit held', required: 'No', help: 'Same, for the water deposit.' },
  { key: 'rent_arrears', label: 'Rent owed today', required: 'No', help: 'Unpaid rent brought forward at go-live.' },
  { key: 'water_arrears', label: 'Water owed today', required: 'No', help: 'Unpaid water brought forward.' },
  { key: 'garbage_arrears', label: 'Garbage owed today', required: 'No', help: 'Unpaid garbage brought forward (only if the property charges garbage).' },
  { key: 'co_payer_name', label: 'Co-payer name', required: 'No', help: 'Someone else who may pay this rent (prints as an "OR" name).' },
  { key: 'co_payer_phone', label: 'Co-payer phone', required: 'No', help: 'Their number, so their payments are matched too.' },
]

const HEADER = ONBOARD_COLUMNS.map(c => c.key).join(',')

export const ONBOARD_TEMPLATE_CSV = [
  HEADER,
  'G1,W-001,JOHN KAMAU,0722 000 001,18000,2025-07-01,36000,3000,5000,700,300,MARY WANJIKU,0733 000 002',
  'G2,W-002,GRACE NJERI,0722 000 003,15000,2025-08-01,30000,3000,0,0,0,,',
  'SHOP NO.1,W-003,,,,,,,,,,,',
].join('\n') + '\n'

export function templateHref(): string {
  return `data:text/csv;charset=utf-8,${encodeURIComponent(ONBOARD_TEMPLATE_CSV)}`
}
