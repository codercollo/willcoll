import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'

describe('ledgers store', () => {
  const get = vi.fn(); const post = vi.fn(); const put = vi.fn(); const openPdf = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia())
    for (const f of [get, post, put, openPdf]) f.mockReset()
    vi.stubGlobal('ref', ref)
    vi.stubGlobal('useApi', () => ({ get, post, put, openPdf }))
  })
  const store = async () => (await import('~/stores/ledgers')).useLedgers()

  it('saves the rent schedule as a bare array', async () => {
    put.mockResolvedValue({})
    await (await store()).saveRentSchedule('p1', [{ unit_id: 'u1', rent_amount: '15000' }])
    expect(put).toHaveBeenCalledWith('/properties/p1/rent-schedule', [{ unit_id: 'u1', rent_amount: '15000' }])
  })
  it('generates rent for a period query', async () => {
    post.mockResolvedValue({ run: { billed: 3 } })
    const run = await (await store()).generateRent('p1', '2026-09')
    expect(run.billed).toBe(3)
    expect(post).toHaveBeenCalledWith('/properties/p1/rent/generate', undefined, { query: { period: '2026-09' } })
  })
  it('saves water readings under a readings key with the period', async () => {
    put.mockResolvedValue({ water: { rows: [] } })
    await (await store()).saveWater('p1', '2026-09', [{ unit_id: 'u1', current_reading: '200' }])
    expect(put).toHaveBeenCalledWith('/properties/p1/water', { readings: [{ unit_id: 'u1', current_reading: '200' }] }, { query: { period: '2026-09' } })
  })
  it('asks for one unit of a bill only when given', async () => {
    const s = await store()
    await s.openBills('water', 'p1', '2026-08')
    expect(openPdf).toHaveBeenCalledWith('/properties/p1/water/invoices/2026-08.pdf', { query: { unit_id: undefined } })
  })
  it('reverses with a reason and returns the reversal id', async () => {
    post.mockResolvedValue({ reversal_entry_id: 'r1' })
    expect(await (await store()).reverse('e1', 'Cheque bounced')).toBe('r1')
    expect(post).toHaveBeenCalledWith('/ledger-entries/e1/reverse', { reason: 'Cheque bounced' })
  })
  it('posts a payment against the chosen ledger', async () => {
    post.mockResolvedValue({ payment: { payment_id: 'x', receipt: 'MANUAL-1' } })
    await (await store()).recordPayment('u1', { amount: '500', source: 'manual', ledger_type: 'water' })
    expect(post).toHaveBeenCalledWith('/units/u1/rent/payments', { amount: '500', source: 'manual', ledger_type: 'water' })
  })
})
