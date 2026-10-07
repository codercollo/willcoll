import { defineStore } from 'pinia'
import type { StaffInput, StaffMember } from '~/types/staff'

// Staff accounts (owner only). Every call is platform-api /v1/staff.
export const useStaff = defineStore('staff', () => {
  const api = useApi()

  async function list() { return (await api.get<{ staff: StaffMember[] }>('/staff')).staff ?? [] }
  async function invite(input: StaffInput) { return (await api.post<{ staff: StaffMember }>('/staff', input)).staff }
  /** Partial update. status 'disabled' signs the person out everywhere at once. */
  async function update(id: string, patch: StaffInput) { return (await api.patch<{ staff: StaffMember }>(`/staff/${id}`, patch)).staff }
  const setStatus = (id: string, status: 'active' | 'disabled') => update(id, { status })

  return { list, invite, update, setStatus }
})
