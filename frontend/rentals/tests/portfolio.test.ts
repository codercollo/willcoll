import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'
import { moneyRatio, sumMoney } from '~/utils/money'
import { slugify } from '~/utils/slug'
import { parseApiError } from '~/utils/apiError'

describe('money helpers', () => {
  it('sums exactly with no float drift', () => {
    expect(sumMoney(['0.10', '0.20'])).toBe('0.30')
    expect(sumMoney(['169000.00', '68000.00', undefined])).toBe('237000.00')
    expect(sumMoney(['-5.50', '2.25'])).toBe('-3.25')
    expect(sumMoney([])).toBe('0.00')
  })
  it('ratios clamp to 0..1', () => {
    expect(moneyRatio('50.00', '100.00')).toBe(0.5)
    expect(moneyRatio('150.00', '100.00')).toBe(1)
    expect(moneyRatio('5', '0')).toBe(0)
    expect(moneyRatio('-5', '10')).toBe(0)
  })
})

describe('slugify', () => {
  it('makes url-safe slugs', () => {
    expect(slugify("THE RUNDA'S ARCADE")).toBe('the-rundas-arcade')
    expect(slugify('  Kiwi   Place! ')).toBe('kiwi-place')
  })
})

describe('import errors', () => {
  it('keeps the structured rows of a 422', () => {
    const e = parseApiError(422, { error: { message: 'the import has errors', rows: [{ row: 4, field: 'unit_code', message: 'repeats row 2' }] } })
    expect(e.message).toBe('the import has errors')
    expect((e.details?.rows as unknown[]).length).toBe(1)
    expect(e.fields).toEqual({})
  })
})

describe('units + properties stores', () => {
  const get = vi.fn(); const post = vi.fn(); const patch = vi.fn(); const put = vi.fn(); const del = vi.fn(); const request = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia())
    for (const f of [get, post, patch, put, del, request]) f.mockReset()
    vi.stubGlobal('ref', ref)
    vi.stubGlobal('useApi', () => ({ get, post, patch, put, del, request }))
  })

  it('lists properties for a period', async () => {
    get.mockResolvedValue({ properties: [{ id: 'p1' }], metadata: { total_records: 1 } })
    const { useProperties } = await import('~/stores/properties')
    const s = useProperties()
    await s.fetchList('2026-09')
    expect(get).toHaveBeenCalledWith('/properties', { period: '2026-09', page: 1, page_size: 100 })
    expect(s.list).toHaveLength(1)
  })

  it('archiving removes it from the list', async () => {
    get.mockResolvedValue({ properties: [{ id: 'p1' }, { id: 'p2' }], metadata: {} })
    del.mockResolvedValue({})
    const { useProperties } = await import('~/stores/properties')
    const s = useProperties()
    await s.fetchList()
    await s.archive('p1')
    expect(s.list.map(p => p.id)).toEqual(['p2'])
  })

  it('an update keeps the loaded summary', async () => {
    get.mockResolvedValue({ property: { id: 'p1', name: 'A', summary: { period: '2026-09' } } })
    patch.mockResolvedValue({ property: { id: 'p1', name: 'B' } })
    const { useProperties } = await import('~/stores/properties')
    const s = useProperties()
    await s.fetchOne('p1')
    await s.update('p1', { name: 'B' })
    expect(s.current?.name).toBe('B')
    expect(s.current?.summary?.period).toBe('2026-09')
  })

  it('sends null to clear a print theme', async () => {
    put.mockResolvedValue({ property: { id: 'p1' } })
    const { useProperties } = await import('~/stores/properties')
    await useProperties().setPrintTheme('p1', null)
    expect(put).toHaveBeenCalledWith('/properties/p1/print-theme', { print_theme: null })
  })

  it('garbage toggle omits the fee when only switching off', async () => {
    patch.mockResolvedValue({ property: { id: 'p1' } })
    const { useProperties } = await import('~/stores/properties')
    await useProperties().setGarbage('p1', false)
    expect(patch).toHaveBeenCalledWith('/properties/p1/garbage', { enabled: false })
  })

  it('csv import is a dry run only when asked', async () => {
    request.mockResolvedValue({ valid_rows: 2 })
    const { useUnits } = await import('~/stores/units')
    const file = new File(['unit_code\nG1\n'], 'u.csv', { type: 'text/csv' })
    await useUnits().importCsv('p1', file, true)
    expect(request.mock.calls[0]![1].query).toEqual({ dry_run: 'true' })
    await useUnits().importCsv('p1', file, false)
    expect(request.mock.calls[1]![1].query).toEqual({ dry_run: undefined })
  })

  it('creates a lease with payers', async () => {
    post.mockResolvedValue({ lease: { id: 'l1' } })
    const { useUnits } = await import('~/stores/units')
    const l = await useUnits().createLease('u1', { tenant_name: 'A', primary_phone: '+254722000000', rent_amount: '1000', rent_deposit_amount: '1000', water_deposit_amount: '0', start_date: '2026-09-01', payers: [{ name: 'B' }] })
    expect(l.id).toBe('l1')
    expect(post.mock.calls[0]![0]).toBe('/units/u1/leases')
  })
})

import { cmpMoney, fromCents, subMoney } from '~/utils/money'

describe('exact money arithmetic for the allocation split', () => {
  it('subtracts without float drift', () => {
    expect(subMoney('6500.00', '0.10')).toBe('6499.90')
    expect(subMoney('0.30', '0.10')).toBe('0.20')
    expect(subMoney('100', '100.00')).toBe('0.00')
    expect(subMoney('10', '12.50')).toBe('-2.50')
  })
  it('compares', () => {
    expect(cmpMoney('1.00', '1')).toBe(0)
    expect(cmpMoney('0.99', '1.00')).toBeLessThan(0)
    expect(cmpMoney('2', '1.99')).toBeGreaterThan(0)
  })
  it('formats cents', () => expect(fromCents(-5)).toBe('-0.05'))
})

describe('payments store', () => {
  const get = vi.fn(); const post = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia()); get.mockReset(); post.mockReset()
    vi.stubGlobal('ref', ref); vi.stubGlobal('useApi', () => ({ get, post }))
  })
  it('keeps the waiting count from the queue metadata', async () => {
    get.mockResolvedValue({ payments: [{ id: 'a' }], metadata: { total_records: 8 } })
    const { usePayments } = await import('~/stores/payments')
    const s = usePayments()
    await s.fetchQueue()
    expect(s.waiting).toBe(8)
    expect(s.queue).toHaveLength(1)
  })
  it('places a payment with its split', async () => {
    post.mockResolvedValue({})
    const { usePayments } = await import('~/stores/payments')
    await usePayments().allocate('p1', 'u1', [{ ledger_type: 'rent', amount: '2500.00' }])
    expect(post).toHaveBeenCalledWith('/payments/p1/allocate', { unit_id: 'u1', allocations: [{ ledger_type: 'rent', amount: '2500.00' }] })
  })
  it('confirms by id', async () => {
    post.mockResolvedValue({})
    const { usePayments } = await import('~/stores/payments')
    await usePayments().confirm('p1')
    expect(post).toHaveBeenCalledWith('/payments/p1/confirm')
  })
})
