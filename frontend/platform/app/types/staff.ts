// A staff member as GET/POST/PATCH /v1/staff return one (system-design A.1).
export type StaffRole = 'owner' | 'accountant' | 'officer' | 'viewer'
export type StaffStatus = 'invited' | 'active' | 'disabled'

export interface StaffMember {
  id: string
  name: string
  email: string
  phone?: string | null
  role: StaffRole
  module_access: string[]
  status: StaffStatus
  created_at: string
}

export interface StaffInput { name?: string; email?: string; phone?: string; role?: string; module_access?: string[]; status?: StaffStatus }

export const STAFF_ROLES: { value: StaffRole; label: string; help: string }[] = [
  { value: 'viewer', label: 'Viewer', help: 'Can look, cannot change anything.' },
  { value: 'officer', label: 'Officer', help: 'Day-to-day work: readings, tenants, leases.' },
  { value: 'accountant', label: 'Accountant', help: 'Officer, plus payments and ledger corrections.' },
  { value: 'owner', label: 'Owner', help: 'Everything, including staff, products and billing.' },
]
