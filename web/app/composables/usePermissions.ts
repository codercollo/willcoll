export type UserRole = 'manager' | 'agent' | 'landlord'

export interface SessionClaims {
  user_id: string
  role: UserRole
  organization_id: string
}

export function usePermissions() {
  const claims = useState<SessionClaims | null>('session-claims', () => null)

  const role = computed<UserRole>(() => claims.value?.role ?? 'manager')
  const isManager = computed(() => role.value === 'manager')
  const isAgent = computed(() => role.value === 'agent')
  const isLandlord = computed(() => role.value === 'landlord')

  function hasRole(...roles: UserRole[]) {
    return roles.includes(role.value)
  }

  // PBAC grant checks are intentionally stubbed client-side until a /v1/me
  // endpoint can return the full session shape. The server remains the only
  // authoritative gate.
  function canRecordPayments(_propertyId?: string) {
    return hasRole('manager', 'agent')
  }

  function setClaims(next: SessionClaims | null) {
    claims.value = next
  }

  return { claims, role, isManager, isAgent, isLandlord, hasRole, canRecordPayments, setClaims }
}
