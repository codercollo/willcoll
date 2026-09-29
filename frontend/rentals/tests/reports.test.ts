import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { reportColumns } from '~/utils/reportColumns'
import { formatDay } from '~/utils/date'

describe('reportColumns', () => {
  it('matches the printed schedule with garbage on', () => {
    expect(reportColumns(true).map(c => c.label)).toEqual(['Hse No.', 'Tenant', 'Rent Paid', 'Water Bills Paid', 'Garbage Paid', 'Rent Deposit', 'Water Deposit', 'Total'])
  })
  it('omits the garbage column entirely when off', () => {
    const cols = reportColumns(false)
    expect(cols.map(c => c.key)).not.toContain('garbage')
    expect(cols).toHaveLength(7)
  })
})

describe('formatDay', () => {
  it('keeps the Kenya calendar day, whatever the browser zone', () => {
    expect(formatDay('2026-08-04T00:00:00Z')).toBe('04/08')
    expect(formatDay('')).toBe('')
  })
})

describe('reports store', () => {
  const get = vi.fn(); const put = vi.fn(); const post = vi.fn(); const openPdf = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia()); for (const f of [get, put, post, openPdf]) f.mockReset()
    vi.stubGlobal('useApi', () => ({ get, put, post, openPdf }))
  })
  const store = async () => (await import('~/stores/reports')).useReports()

  it('fetches the sanity checks + note preview', async () => {
    get.mockResolvedValue({ checks: { failures: null, info: null }, report: {} })
    await (await store()).checks('p1', '2026-08')
    expect(get).toHaveBeenCalledWith('/properties/p1/reports/2026-08/checks')
  })
  it('confirms by POST with no body', async () => {
    post.mockResolvedValue({ report: { confirmed: true } })
    await (await store()).confirm('p1', '2026-08')
    expect(post).toHaveBeenCalledWith('/properties/p1/reports/2026-08/confirm')
  })
  it('fetches the plot-meter reading form state', async () => {
    get.mockResolvedValue({ plot_meter: { previous_reading: '100', current_reading: null, units_consumed: null, reading_date: null } })
    await (await store()).plotMeter('p1', '2026-08')
    expect(get).toHaveBeenCalledWith('/properties/p1/reports/2026-08/plot-meter')
  })
  it('sends the current reading as a decimal string, with no previous override once one is on file', async () => {
    await (await store()).setPlotMeter('p1', '2026-08', '1234.50')
    expect(put).toHaveBeenCalledWith('/properties/p1/reports/2026-08/plot-meter', { current_reading: '1234.50', previous_reading: undefined })
  })
  it('sends a previous override for the first-ever reading', async () => {
    await (await store()).setPlotMeter('p1', '2026-08', '1234.50', '1000')
    expect(put).toHaveBeenCalledWith('/properties/p1/reports/2026-08/plot-meter', { current_reading: '1234.50', previous_reading: '1000' })
  })
  it('generates the schedule by POST', async () => {
    await (await store()).openSchedule('p1', '2026-08')
    expect(openPdf).toHaveBeenCalledWith('/properties/p1/reports/2026-08/generate', { method: 'POST' })
  })
  it('asks for one unit receipt only when given', async () => {
    const s = await store()
    await s.openReceipts('p1', '2026-08')
    await s.openReceipts('p1', '2026-08', 'u1')
    expect(openPdf.mock.calls[0]![1]).toEqual({ method: 'GET', query: { unit_id: undefined } })
    expect(openPdf.mock.calls[1]![1]).toEqual({ method: 'GET', query: { unit_id: 'u1' } })
  })
})
