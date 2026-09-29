import { defineStore } from 'pinia'
import type { ImportRowError, Lease, LeasePayer, PageMeta, Unit } from '~/types/api'

export interface LeaseInput {
  tenant_name: string
  primary_phone: string
  rent_amount: string
  rent_deposit_amount: string
  water_deposit_amount: string
  electricity_deposit_amount?: string
  start_date: string
  payers?: { name: string; phone?: string }[]
}

export interface UnitQuery { status?: string; search?: string; sort?: string; page?: number; page_size?: number }

export const useUnits = defineStore('units', () => {
  const api = useApi()
  const list = ref<Unit[]>([])
  const meta = ref<Partial<PageMeta> | null>(null)

  async function fetchList(propertyId: string, q: UnitQuery = {}) {
    const res = await api.get<{ units: Unit[]; metadata: Partial<PageMeta> }>(`/properties/${propertyId}/units`, { page_size: 24, sort: 'unit_code', ...q })
    list.value = res.units ?? []
    meta.value = res.metadata
  }

  /** Every unit of a property (for pickers). Does not touch the paged list state. */
  async function all(propertyId: string) {
    const res = await api.get<{ units: Unit[] }>(`/properties/${propertyId}/units`, { page_size: 100, sort: 'unit_code' })
    return res.units ?? []
  }

  async function create(propertyId: string, input: { unit_code: string; meter_number?: string }) {
    return (await api.post<{ unit: Unit }>(`/properties/${propertyId}/units`, input)).unit
  }

  async function get(id: string) {
    return (await api.get<{ unit: Unit }>(`/units/${id}`)).unit
  }

  async function update(id: string, patch: { unit_code?: string; meter_number?: string }) {
    return (await api.patch<{ unit: Unit }>(`/units/${id}`, patch)).unit
  }

  /** Validate a CSV without writing anything. Returns the count of valid rows. */
  async function importCsv(propertyId: string, file: File, dryRun: boolean) {
    const form = new FormData()
    form.append('file', file)
    return api.request<{ valid_rows?: number; imported?: number }>(`/properties/${propertyId}/units/import`, {
      method: 'POST', rawBody: form, query: { dry_run: dryRun ? 'true' : undefined },
    })
  }

  async function leases(unitId: string) {
    return (await api.get<{ leases: Lease[] }>(`/units/${unitId}/leases`)).leases ?? []
  }

  async function createLease(unitId: string, input: LeaseInput) {
    return (await api.post<{ lease: Lease }>(`/units/${unitId}/leases`, input)).lease
  }

  async function updateLease(id: string, patch: { tenant_name?: string; primary_phone?: string; rent_amount?: string; end_date?: string; status?: 'active' | 'terminated' }) {
    return (await api.patch<{ lease: Lease }>(`/leases/${id}`, patch)).lease
  }

  async function addPayer(leaseId: string, payer: { name: string; phone?: string }) {
    return (await api.post<{ payer: LeasePayer }>(`/leases/${leaseId}/payers`, payer)).payer
  }

  async function removePayer(leaseId: string, payerId: string) {
    await api.del(`/leases/${leaseId}/payers/${payerId}`)
  }

  return { list, meta, fetchList, all, create, get, update, importCsv, leases, createLease, updateLease, addPayer, removePayer }
})

/** Row errors of a rejected import, as sent in the 422 body. */
export function importRows(fields: unknown): ImportRowError[] {
  return Array.isArray(fields) ? (fields as ImportRowError[]) : []
}
