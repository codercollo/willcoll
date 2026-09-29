import { defineStore } from 'pinia'
import type { Landlord, PageMeta, Property, PrintTheme } from '~/types/api'

export interface PropertyInput {
  landlord_id: string
  name: string
  location: string
  slug: string
  garbage_enabled: boolean
  garbage_fee: string
  water_rate_per_unit: string
  management_fee_percent: number
  payhero_channel_id?: string
}

export type PropertyPatch = Partial<Pick<PropertyInput, 'landlord_id' | 'name' | 'location' | 'slug' | 'water_rate_per_unit' | 'management_fee_percent' | 'payhero_channel_id'>>
export type LandlordInput = Omit<Landlord, 'id' | 'created_at'>

export const useProperties = defineStore('properties', () => {
  const api = useApi()
  const list = ref<Property[]>([])
  const meta = ref<Partial<PageMeta> | null>(null)
  const landlords = ref<Landlord[]>([])
  const current = ref<Property | null>(null)

  async function fetchList(period?: string, page = 1) {
    const res = await api.get<{ properties: Property[]; metadata: Partial<PageMeta> }>('/properties', { period, page, page_size: 100 })
    list.value = res.properties ?? []
    meta.value = res.metadata
  }

  async function fetchOne(id: string, period?: string) {
    const res = await api.get<{ property: Property }>(`/properties/${id}`, { period })
    current.value = res.property
    return res.property
  }

  async function create(input: PropertyInput) {
    const res = await api.post<{ property: Property }>('/properties', input)
    return res.property
  }

  async function update(id: string, patch: PropertyPatch) {
    const res = await api.patch<{ property: Property }>(`/properties/${id}`, patch)
    current.value = { ...res.property, summary: current.value?.summary }
    return res.property
  }

  async function setGarbage(id: string, enabled: boolean, fee?: string) {
    const res = await api.patch<{ property: Property }>(`/properties/${id}/garbage`, fee === undefined ? { enabled } : { enabled, fee })
    current.value = { ...res.property, summary: current.value?.summary }
    return res.property
  }

  async function setElectricity(id: string, enabled: boolean, depositAmount?: string) {
    const res = await api.patch<{ property: Property; leases_charged: number }>(`/properties/${id}/electricity`, depositAmount === undefined ? { enabled } : { enabled, deposit_amount: depositAmount })
    current.value = { ...res.property, summary: current.value?.summary }
    return res
  }

  async function listPaymentChannelBanks() {
    return (await api.get<{ banks: { name: string; paybill: number }[] }>('/payment-channel-banks')).banks ?? []
  }

  async function registerPaymentChannel(id: string, input: {
    type: 'bank' | 'paybill' | 'till'
    bank?: string
    short_code?: number
    account_number?: string
    description: string
  }) {
    const res = await api.post<{ channel: { id: number; account_number: string; reused: boolean }; property: Property }>(`/properties/${id}/payment-channel`, input)
    current.value = { ...res.property, summary: current.value?.summary }
    return res
  }

  async function paymentChannelStatus(id: string) {
    return api.get<{ status: 'active' | 'inactive' | 'missing'; channel?: { id: number; channel_type: string; account_number: string } }>(`/properties/${id}/payment-channel/status`)
  }

  async function sendPaymentChannelTest(id: string, phone: string) {
    return api.post<{ message: string; reference: string; status: string }>(`/properties/${id}/payment-channel/test`, { phone })
  }

  async function setPrintTheme(id: string, theme: PrintTheme | null) {
    const res = await api.put<{ property: Property }>(`/properties/${id}/print-theme`, { print_theme: theme })
    current.value = { ...res.property, summary: current.value?.summary }
    return res.property
  }

  async function archive(id: string) {
    await api.del(`/properties/${id}`)
    list.value = list.value.filter(p => p.id !== id)
    current.value = null
  }

  async function fetchLandlords() {
    const res = await api.get<{ landlords: Landlord[] }>('/landlords', { page_size: 100, sort: 'name' })
    landlords.value = res.landlords ?? []
    return landlords.value
  }

  async function createLandlord(input: LandlordInput) {
    const res = await api.post<{ landlord: Landlord }>('/landlords', input)
    landlords.value = [...landlords.value, res.landlord]
    return res.landlord
  }

  async function updateLandlord(id: string, patch: Partial<LandlordInput>) {
    const res = await api.patch<{ landlord: Landlord }>(`/landlords/${id}`, patch)
    landlords.value = landlords.value.map(l => (l.id === id ? res.landlord : l))
    return res.landlord
  }

  return { list, meta, landlords, current, fetchList, fetchOne, create, update, setGarbage, setElectricity, setPrintTheme, archive, fetchLandlords, createLandlord, updateLandlord, listPaymentChannelBanks, registerPaymentChannel, paymentChannelStatus, sendPaymentChannelTest }
})
