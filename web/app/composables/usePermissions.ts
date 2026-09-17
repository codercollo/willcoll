export type UserRole = 'manager' | 'agent' | 'landlord'

export interface SessionClaims {
  user_id: string
  role: UserRole
  organization_id: string
}

// One property's PBAC bitset (spec §2.2) — shape matches POST /v1/agent-grants.
export interface PropertyGrant {
  property_id: string
  can_record_payments: boolean
  can_edit_leases: boolean
  can_edit_unit_pricing: boolean
  can_void_payments: boolean
  can_view_financial_reports: boolean
  can_manage_meter_readings: boolean
}

// RBAC default bundle per role (mirrors internal/auth/permissions.go's
// rolePermissions table, spec §2.1). Agent's PBAC-narrowable actions are
// listed here as the role's ceiling; whether a specific Agent actually has
// them on a specific property is decided by hasGrant below, never assumed.
const RBAC: Record<UserRole, Set<string>> = {
  manager: new Set([
    'view_portfolio', 'record_payment', 'record_meter_reading', 'issue_invoice',
    'edit_unit_pricing', 'edit_lease', 'manage_property', 'invite_agent',
    'set_agent_grant', 'view_ledger', 'void_payment', 'view_financial_reports',
    'configure_sms_templates', 'system_administration',
  ]),
  agent: new Set(['view_portfolio', 'record_payment', 'record_meter_reading', 'issue_invoice', 'view_ledger']),
  landlord: new Set(['view_portfolio', 'manage_property', 'view_ledger', 'view_financial_reports', 'configure_sms_templates']),
}

const PBAC_ACTIONS = ['can_record_payments', 'can_edit_leases', 'can_edit_unit_pricing', 'can_void_payments', 'can_view_financial_reports', 'can_manage_meter_readings'] as const
export type PbacAction = (typeof PBAC_ACTIONS)[number]

export function usePermissions() {
  const claims = useState<SessionClaims | null>('session-claims', () => null)
  const role = computed<UserRole | null>(() => claims.value?.role ?? null)

  function hasRole(...roles: UserRole[]) {
    return role.value !== null && roles.includes(role.value)
  }

  function hasBaseAction(action: string) {
    return role.value !== null && RBAC[role.value].has(action)
  }

  // For a PBAC-narrowable action: Manager/Landlord get it from the RBAC
  // table directly. An Agent needs an actual grant row for that property —
  // no grant loaded means deny, never a default-allow guess.
  function canOnProperty(action: PbacAction, grant: PropertyGrant | null | undefined) {
    if (!role.value) return false
    if (role.value === 'manager') return true
    if (role.value !== 'agent') return false
    return grant?.[action] === true
  }

  function setClaims(next: SessionClaims | null) {
    claims.value = next
  }

  return {
    claims,
    role,
    isManager: computed(() => role.value === 'manager'),
    isAgent: computed(() => role.value === 'agent'),
    isLandlord: computed(() => role.value === 'landlord'),
    hasRole,
    hasBaseAction,
    canOnProperty,
    setClaims,
  }
}
