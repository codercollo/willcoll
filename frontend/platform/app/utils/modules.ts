import { trialDaysLeft, type ModuleRow } from '#base/utils/appLinks'
import type { BadgeStatus } from '#base/components/ui/Badge.vue'

// What a firm's standing in one product reads as, on the Products cards and the
// Modules page. One mapping, so the two never disagree.
export type ModuleState = 'none' | 'trial' | 'trial_ended' | 'active' | 'requested' | 'suspended'

export interface ModuleView { state: ModuleState; label: string; badge: BadgeStatus; daysLeft: number | null }

export function moduleView(m: ModuleRow | null | undefined, now: Date = new Date()): ModuleView {
  if (!m) return { state: 'none', label: 'Not started', badge: 'vacant', daysLeft: null }
  switch (m.status) {
    case 'active': return { state: 'active', label: 'Active', badge: 'paid', daysLeft: null }
    case 'suspended': return { state: 'suspended', label: 'Suspended', badge: 'arrears', daysLeft: null }
    case 'requested': return { state: 'requested', label: 'Requested', badge: 'pending', daysLeft: null }
    case 'trialing': {
      const left = trialDaysLeft(m.trial_ends_at, now)
      if (left === 0) return { state: 'trial_ended', label: 'Trial ended', badge: 'arrears', daysLeft: 0 }
      return { state: 'trial', label: left === null ? 'Trial' : `Trial · ${left} ${left === 1 ? 'day' : 'days'} left`, badge: 'info', daysLeft: left }
    }
    default: return { state: 'none', label: m.status, badge: 'vacant', daysLeft: null }
  }
}
