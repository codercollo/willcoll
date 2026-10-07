import { describe, expect, it } from 'vitest'
import { moduleView } from '~/utils/modules'

const now = new Date('2026-10-07T09:00:00Z')

describe('moduleView', () => {
  it('a product the firm never started', () => {
    expect(moduleView(undefined, now)).toMatchObject({ state: 'none', label: 'Not started' })
    expect(moduleView(null, now).state).toBe('none')
  })
  it('a running trial shows the days left', () => {
    expect(moduleView({ product: 'rentals', status: 'trialing', trial_ends_at: '2026-10-12T09:00:00Z' }, now)).toMatchObject({ state: 'trial', label: 'Trial · 5 days left', daysLeft: 5 })
    expect(moduleView({ product: 'rentals', status: 'trialing', trial_ends_at: '2026-10-08T09:00:00Z' }, now).label).toBe('Trial · 1 day left')
  })
  it('an ended trial is flagged, not shown as running', () => {
    expect(moduleView({ product: 'rentals', status: 'trialing', trial_ends_at: '2026-10-01T09:00:00Z' }, now)).toMatchObject({ state: 'trial_ended', badge: 'arrears' })
  })
  it('active, suspended and requested', () => {
    expect(moduleView({ product: 'rentals', status: 'active' }, now).state).toBe('active')
    expect(moduleView({ product: 'rentals', status: 'suspended' }, now).state).toBe('suspended')
    expect(moduleView({ product: 'estates', status: 'requested' }, now).state).toBe('requested')
  })
})
