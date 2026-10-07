// Rentals' own pay-page vocabulary. The amount-form helpers (whole shillings,
// totals, phone check) are product-agnostic and live in @willcoll/base's
// utils/payForm.ts; what a line is CALLED and which lines can't be paid
// ahead is Rentals-specific.
const LABELS: Record<string, string> = {
  RENT: 'Rent', WATER: 'Water', GARBAGE: 'Garbage', RENT_DEPOSIT: 'Rent deposit', WATER_DEPOSIT: 'Water deposit',
  ELECTRICITY_DEPOSIT: 'Electricity deposit',
}
export const payLabel = (type: string) => LABELS[type.toUpperCase()] ?? type

// A one-off refundable deposit can't be paid ahead (system-design.txt
// 3.2.1): the input is capped at what's outstanding and disabled once it's
// fully paid. Rent/water/garbage may still be overpaid (an advance).
const DEPOSIT_TYPES = new Set(['RENT_DEPOSIT', 'WATER_DEPOSIT', 'ELECTRICITY_DEPOSIT'])
export const isDepositType = (type: string) => DEPOSIT_TYPES.has(type.toUpperCase())
