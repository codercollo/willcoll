import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { computed, ref } from 'vue'
import { ONBOARD_COLUMNS, ONBOARD_TEMPLATE_CSV } from '~/utils/onboardTemplate'

describe('onboarding template', () => {
  it('has every column the API accepts, header first', () => {
    const header = ONBOARD_TEMPLATE_CSV.split('\n')[0]!.split(',')
    expect(header).toEqual(ONBOARD_COLUMNS.map(c => c.key))
    expect(header).toContain('unit_code')
    expect(header).toContain('rent_arrears')
    expect(header).toHaveLength(13)
  })
  it('its example rows all have the same number of cells as the header', () => {
    const lines = ONBOARD_TEMPLATE_CSV.trim().split('\n')
    for (const l of lines) expect(l.split(',')).toHaveLength(13)
  })
  it('includes a vacant-unit example (a row with only the unit)', () => {
    expect(ONBOARD_TEMPLATE_CSV).toMatch(/^SHOP NO\.1,W-003,,,,,,,,,,,$/m)
  })
})

describe('onboarding store', () => {
  const get = vi.fn(); const request = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia()); get.mockReset(); request.mockReset()
    vi.stubGlobal('ref', ref); vi.stubGlobal('computed', computed)
    vi.stubGlobal('useApi', () => ({ get, request }))
  })
  const store = async () => (await import('~/stores/onboarding')).useOnboarding()
  const steps = { steps: [], complete: false, next: 'landlord', done: 1, total: 5 }

  it('counts the required steps still to do for the sidebar badge', async () => {
    get.mockResolvedValue({ onboarding: steps })
    const s = await store()
    expect(s.remaining).toBe(0) // not loaded yet
    await s.fetchStatus()
    expect(s.remaining).toBe(4)
  })
  it('has no badge once complete', async () => {
    get.mockResolvedValue({ onboarding: { ...steps, complete: true, done: 5 } })
    const s = await store()
    await s.fetchStatus()
    expect(s.remaining).toBe(0)
  })
  it('a check is a dry run with the as-at date; the real import is not', async () => {
    request.mockResolvedValue({ summary: { leases_created: 3 }, as_at: '2026-10-01' })
    const s = await store()
    const file = new File(['unit_code\nA1\n'], 'x.csv', { type: 'text/csv' })
    await s.importSheet('p1', file, { dryRun: true, asAt: '2026-10-01' })
    expect(request).toHaveBeenLastCalledWith('/properties/p1/onboarding/import', expect.objectContaining({ method: 'POST', query: { dry_run: 'true', as_at: '2026-10-01' } }))
    request.mockResolvedValue({ imported: { leases_created: 3 }, as_at: '2026-10-01' })
    const r = await s.importSheet('p1', file, { dryRun: false, asAt: '2026-10-01' })
    expect(request).toHaveBeenLastCalledWith('/properties/p1/onboarding/import', expect.objectContaining({ query: { dry_run: undefined, as_at: '2026-10-01' } }))
    expect(r.summary.leases_created).toBe(3)
  })
})
