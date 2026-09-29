import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'
import { formatBytes } from '~/utils/format'

describe('formatBytes', () => {
  it('formats sizes', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(11824275)).toBe('11 MB')
    expect(formatBytes(1572864)).toBe('1.5 MB')
    expect(formatBytes(221184)).toBe('216 KB')
    expect(formatBytes(null)).toBe('0 B')
  })
})

describe('admin store', () => {
  const get = vi.fn(); const post = vi.fn(); const patch = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia()); for (const f of [get, post, patch]) f.mockReset()
    vi.stubGlobal('ref', ref); vi.stubGlobal('useApi', () => ({ get, post, patch }))
  })
  const store = async () => (await import('~/stores/admin')).useAdmin()

  it('lists managers filtered by status', async () => {
    get.mockResolvedValue({ managers: [{ id: 'm1' }], metadata: { total_records: 1 } })
    const r = await (await store()).managers({ status: 'suspended', page: 2 })
    expect(get).toHaveBeenCalledWith('/admin/managers', { page_size: 20, status: 'suspended', page: 2 })
    expect(r.rows).toHaveLength(1)
  })
  it('suspends and reinstates by id, with no body', async () => {
    post.mockResolvedValue({})
    const s = await store()
    await s.suspend('m1'); await s.reinstate('m1')
    expect(post).toHaveBeenNthCalledWith(1, '/admin/managers/m1/suspend')
    expect(post).toHaveBeenNthCalledWith(2, '/admin/managers/m1/reinstate')
  })
  it('creates a plan with a whole-shilling price string', async () => {
    post.mockResolvedValue({ plan: { id: 'p1' } })
    await (await store()).createPlan({ name: 'Quarterly Pro', price: '9000', billing_interval: 'quarterly', unit_cap: 150 })
    expect(post).toHaveBeenCalledWith('/admin/plans', { name: 'Quarterly Pro', price: '9000', billing_interval: 'quarterly', unit_cap: 150 })
  })
  it('patches only what changed and can clear the unit cap', async () => {
    patch.mockResolvedValue({ plan: { id: 'p1' } })
    await (await store()).updatePlan('p1', { unit_cap: null })
    expect(patch).toHaveBeenCalledWith('/admin/plans/p1', { unit_cap: null })
  })
})
