import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'

// Hand-entered cash waits for a decision (docs/security/c4-matrix-fixes.md item 3):
// the store carries the pending counts the dashboard notice reads, and rejecting
// sends a reason.
describe('payments store: pending cash payments', () => {
  const get = vi.fn(); const post = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia()); for (const f of [get, post]) f.mockReset()
    vi.stubGlobal('ref', ref)
    vi.stubGlobal('useApi', () => ({ get, post }))
  })
  const store = async () => (await import('~/stores/payments')).usePayments()

  it('reads the pending counts from the review response', async () => {
    get.mockResolvedValue({
      payments: [{ id: 'p1', status: 'pending', recorded_by_role: 'officer' }],
      metadata: { total_records: 3 },
      summary: { pending: 2, pending_over_48h: 1 },
    })
    const s = await store()
    await s.fetchQueue()
    expect(s.waiting).toBe(3)
    expect(s.pending).toBe(2)
    expect(s.pendingOver48h).toBe(1)
    expect(s.queue[0]!.status).toBe('pending')
  })

  it('refreshCount updates the same counts with a page of one', async () => {
    get.mockResolvedValue({ payments: [], metadata: { total_records: 0 }, summary: { pending: 0, pending_over_48h: 4 } })
    const s = await store()
    await s.refreshCount()
    expect(get).toHaveBeenCalledWith('/payments/review', { page: 1, page_size: 1 })
    expect(s.pendingOver48h).toBe(4)
  })

  it('an older API response without a summary leaves the counts at zero', async () => {
    get.mockResolvedValue({ payments: [], metadata: { total_records: 0 } })
    const s = await store()
    await s.refreshCount()
    expect(s.pending).toBe(0)
    expect(s.pendingOver48h).toBe(0)
  })

  it('confirms and rejects through the payment routes, the rejection with its reason', async () => {
    post.mockResolvedValue({})
    const s = await store()
    await s.confirm('p1')
    expect(post).toHaveBeenCalledWith('/payments/p1/confirm')
    await s.reject('p2', 'no cash was handed over')
    expect(post).toHaveBeenCalledWith('/payments/p2/reject', { reason: 'no cash was handed over' })
  })
})
