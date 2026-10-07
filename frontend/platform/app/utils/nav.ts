import type { NavItem } from '#base/components/ui/AppShell.vue'

// Which of platform's pages a signed-in firm sees. Staff are sub-logins
// (system-design A.1): only the firm's own login and staff whose role is
// "owner" manage staff, products and billing; everyone else gets their
// products and their own account. platform-api enforces the same split
// (requireOwner); hiding the links and redirecting is the courtesy on top.

/** Pages (and everything under them) that need an owner. */
export const OWNER_ONLY_PATHS = ['/staff', '/modules', '/billing']

const startsWithPath = (path: string, prefix: string) => path === prefix || path.startsWith(prefix + '/')

/** May this person open `path`? Admins use /admin pages only; they never get here. */
export function canVisit(path: string, isOwner: boolean): boolean {
  if (isOwner) return true
  return !OWNER_ONLY_PATHS.some(p => startsWithPath(path, p))
}

interface PlatformNavItem extends NavItem { ownerOnly?: boolean }

const MANAGER_NAV: PlatformNavItem[] = [
  { label: 'Products', to: '/', icon: 'lucide:layout-grid' },
  { label: 'Modules', to: '/modules', icon: 'lucide:blocks', ownerOnly: true },
  { label: 'Staff', to: '/staff', icon: 'lucide:users', ownerOnly: true },
  { label: 'Billing', to: '/billing', icon: 'lucide:credit-card', ownerOnly: true },
  { label: 'Account', to: '/account', icon: 'lucide:user-round' },
]

/** The sidebar for a firm login or its staff. */
export function managerNav(isOwner: boolean): NavItem[] {
  return MANAGER_NAV.filter(i => isOwner || !i.ownerOnly).map(({ ownerOnly: _ownerOnly, ...item }) => item)
}
