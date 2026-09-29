// Columns of the ALL IN ONE PAYMENTS SCHEDULE preview, in the printed order
// (Hse No. | Tenant | Rent Paid | Water Bills Paid | [Garbage Paid] | Rent
// Deposit | Water Deposit | Total). Garbage is omitted, not zeroed, when the
// property does not charge it.
export interface ReportColumn { key: string; label: string; align?: 'left' | 'right' }

export function reportColumns(garbageEnabled: boolean, electricityDepositShown = false): ReportColumn[] {
  const cols: ReportColumn[] = [
    { key: 'house_no', label: 'Hse No.' }, { key: 'tenants', label: 'Tenant' },
    { key: 'rent', label: 'Rent Paid', align: 'right' }, { key: 'water', label: 'Water Bills Paid', align: 'right' },
  ]
  if (garbageEnabled) cols.push({ key: 'garbage', label: 'Garbage Paid', align: 'right' })
  cols.push(
    { key: 'rent_deposit', label: 'Rent Deposit', align: 'right' },
    { key: 'water_deposit', label: 'Water Deposit', align: 'right' },
  )
  if (electricityDepositShown) cols.push({ key: 'electricity_deposit', label: 'Electricity Deposit', align: 'right' })
  cols.push({ key: 'total', label: 'Total', align: 'right' })
  return cols
}
