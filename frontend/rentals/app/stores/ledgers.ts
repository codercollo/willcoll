import { defineStore } from 'pinia'
import type {
  ChargeInput, ElectricityDeposits, GarbagePreview, LedgerPage, LedgerType, PaymentInput, RentOverviewRow, RentRun, WaterGrid, WaterReadingInput, WaterRun,
} from '~/types/billing'

// Thin, stateless wrapper over the billing and ledger endpoints. Screens keep
// the grids they show; the server is always the source of every computed value.
export const useLedgers = defineStore('ledgers', () => {
  const api = useApi()

  // ---- rent ---------------------------------------------------------------
  async function rentOverview(propertyId: string, period: string) {
    return (await api.get<{ period: string; overview: RentOverviewRow[] }>(`/properties/${propertyId}/rent/overview`, { period })).overview ?? []
  }
  async function generateRent(propertyId: string, period: string) {
    return (await api.post<{ run: RentRun }>(`/properties/${propertyId}/rent/generate`, undefined, { query: { period } })).run
  }
  /** Saves expected rent for several units at once. Posts nothing; future runs use it. */
  async function saveRentSchedule(propertyId: string, rows: { unit_id: string; rent_amount: string }[]) {
    await api.put(`/properties/${propertyId}/rent-schedule`, rows)
  }

  // ---- water --------------------------------------------------------------
  async function waterGrid(propertyId: string, period: string) {
    return (await api.get<{ water: WaterGrid }>(`/properties/${propertyId}/water`, { period })).water
  }
  async function saveWater(propertyId: string, period: string, readings: WaterReadingInput[]) {
    return (await api.put<{ water: WaterGrid }>(`/properties/${propertyId}/water`, { readings }, { query: { period } })).water
  }
  async function generateWater(propertyId: string, period: string) {
    return (await api.post<{ run: WaterRun }>(`/properties/${propertyId}/water/generate`, undefined, { query: { period } })).run
  }

  // ---- garbage ------------------------------------------------------------
  async function garbagePreview(propertyId: string, period: string) {
    return (await api.get<{ garbage: GarbagePreview }>(`/properties/${propertyId}/garbage/generate`, { period })).garbage
  }
  async function generateGarbage(propertyId: string, period: string) {
    return (await api.post<{ run: Record<string, unknown> }>(`/properties/${propertyId}/garbage/generate`, undefined, { query: { period } })).run
  }

  // ---- electricity deposits -------------------------------------------------
  async function electricityDeposits(propertyId: string) {
    return (await api.get<{ electricity: ElectricityDeposits }>(`/properties/${propertyId}/electricity/deposits`)).electricity
  }
  function openElectricityDepositsPDF(propertyId: string) {
    return api.openPdf(`/properties/${propertyId}/electricity/deposits.pdf`)
  }
  function downloadElectricityDepositsCSV(propertyId: string) {
    return api.download(`/properties/${propertyId}/electricity/deposits.csv`, 'electricity-deposits.csv')
  }

  // ---- bills (PDF, nothing stored) ----------------------------------------
  function openBills(kind: 'water' | 'garbage', propertyId: string, period: string, unitId?: string) {
    return api.openPdf(`/properties/${propertyId}/${kind}/invoices/${period}.pdf`, { query: { unit_id: unitId } })
  }

  // ---- unit ledgers -------------------------------------------------------
  async function ledger(unitId: string, type: LedgerType, page = 1, pageSize = 15) {
    return api.get<LedgerPage>(`/units/${unitId}/ledger/${type}`, { page, page_size: pageSize })
  }
  /** Current balance of each requested ledger of a unit (one request each). */
  async function balances(unitId: string, types: LedgerType[]) {
    const out: Partial<Record<LedgerType, string>> = {}
    await Promise.all(types.map(async (t) => { out[t] = (await ledger(unitId, t, 1, 1)).balance.balance }))
    return out
  }
  async function reverse(entryId: string, reason: string) {
    return (await api.post<{ reversal_entry_id: string }>(`/ledger-entries/${entryId}/reverse`, { reason })).reversal_entry_id
  }
  async function recordPayment(unitId: string, input: PaymentInput) {
    return (await api.post<{ payment: { payment_id: string; receipt: string } }>(`/units/${unitId}/rent/payments`, input)).payment
  }
  async function addCharge(unitId: string, input: ChargeInput) {
    await api.post(`/units/${unitId}/rent/charges`, input)
  }

  return {
    rentOverview, generateRent, saveRentSchedule, waterGrid, saveWater, generateWater,
    garbagePreview, generateGarbage, openBills, ledger, balances, reverse, recordPayment, addCharge,
    electricityDeposits, openElectricityDepositsPDF, downloadElectricityDepositsCSV,
  }
})
