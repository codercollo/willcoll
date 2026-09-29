import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { computed, ref } from 'vue'
import SpreadsheetTable from '~/components/property/SpreadsheetTable.vue'
import LedgerTable from '~/components/ledger/LedgerTable.vue'
import Badge from '~/components/ui/Badge.vue'
import Button from '~/components/ui/Button.vue'
import { formatMoney, isNegativeMoney, isZeroMoney } from '~/utils/money'
import { formatDate } from '~/utils/date'
import type { LedgerEntry } from '~/types/billing'

const Icon = { props: ['name'], template: '<i :data-icon="name" />' }

describe('SpreadsheetTable', () => {
  const columns = [{ key: 'code', label: 'House' }, { key: 'amount', label: 'Amount', align: 'right' as const }]
  const rows = [{ id: 'a', code: 'G1', amount: '1,000.00', locked: false }, { id: 'b', code: 'G2', amount: '2,000.00', locked: true }]
  const build = (footer?: Record<string, string>) => mount(SpreadsheetTable, {
    props: { columns, rows, rowKey: (r: { id: string }) => r.id, locked: (r: { locked: boolean }) => r.locked, footer },
    global: { components: { Icon } },
  })

  it('renders headers and a row per record', () => {
    const w = build()
    expect(w.findAll('thead th').map(t => t.text())).toContain('House')
    expect(w.findAll('tbody tr')).toHaveLength(2)
  })
  it('marks only locked rows, with a lock icon and grey fill', () => {
    const w = build()
    const [open, locked] = w.findAll('tbody tr')
    expect(open!.find('[data-icon="lucide:lock"]').exists()).toBe(false)
    expect(locked!.find('[data-icon="lucide:lock"]').exists()).toBe(true)
    expect(locked!.classes()).toContain('bg-gray-50')
  })
  it('shows the totals row only when given a footer', () => {
    expect(build().find('tfoot').exists()).toBe(false)
    const w = build({ amount: '3,000.00' })
    expect(w.find('tfoot').text()).toContain('Totals')
    expect(w.find('tfoot').text()).toContain('3,000.00')
  })
  it('renders a slot for a cell', () => {
    const w = mount(SpreadsheetTable, {
      props: { columns, rows, rowKey: (r: { id: string }) => r.id },
      slots: { 'cell-amount': '<template #cell-amount="{ row }"><input :value="row.amount" aria-label="edit"></template>' },
      global: { components: { Icon } },
    })
    expect(w.findAll('input[aria-label=edit]')).toHaveLength(2)
  })
  it('shows an empty message with no rows', () => {
    const w = mount(SpreadsheetTable, { props: { columns, rows: [], rowKey: () => 'x', empty: 'No units.' }, global: { components: { Icon } } })
    expect(w.text()).toContain('No units.')
  })
})

describe('LedgerTable', () => {
  beforeEach(() => {
    vi.stubGlobal('useCurrency', () => ({ format: formatMoney, isZero: isZeroMoney, isNegative: isNegativeMoney }))
    vi.stubGlobal('formatDate', formatDate)
    vi.stubGlobal('computed', computed) // Nuxt auto-imports it in the real app
  })
  const entry = (o: Partial<LedgerEntry>): LedgerEntry => ({
    id: 'e1', direction: 'DEBIT', amount: '18000.00', reference_type: 'charge', created_at: '2026-09-01T00:00:00Z',
    transaction_type: 'RENT_RUN', description: 'Rent 2026-09', running_balance: '18000.00', reversed: false, ...o,
  })
  const mountIt = (entries: LedgerEntry[]) => mount(LedgerTable, { props: { entries }, global: { components: { UiBadge: Badge, UiButton: Button, Icon }, config: { globalProperties: { formatDate } } } })

  it('puts debits and credits in their own columns', () => {
    const w = mountIt([entry({}), entry({ id: 'e2', direction: 'CREDIT', amount: '5000.00', description: 'Payment', running_balance: '13000.00', transaction_type: 'PAYMENT_POSTING' })])
    const [debitRow, creditRow] = w.findAll('tbody tr')
    const d = debitRow!.findAll('td'); const c = creditRow!.findAll('td')
    expect(d[2]!.text()).toBe('18,000.00'); expect(d[3]!.text()).toBe('')
    expect(c[2]!.text()).toBe(''); expect(c[3]!.text()).toBe('5,000.00')
  })
  it('offers Reverse on a live entry, never on a reversed one or a reversal', () => {
    const w = mountIt([entry({}), entry({ id: 'e2', reversed: true }), entry({ id: 'e3', transaction_type: 'REVERSAL' })])
    const rows = w.findAll('tbody tr')
    expect(rows[0]!.find('button').exists()).toBe(true)
    expect(rows[1]!.find('button').exists()).toBe(false)
    expect(rows[2]!.find('button').exists()).toBe(false)
  })
  it('strikes through and labels a reversed entry', () => {
    const w = mountIt([entry({ reversed: true })])
    expect(w.find('.line-through').exists()).toBe(true)
    expect(w.text()).toContain('Reversed')
  })
  it('emits the entry when Reverse is clicked', async () => {
    const w = mountIt([entry({})])
    await w.find('button').trigger('click')
    expect((w.emitted('reverse')![0]![0] as LedgerEntry).id).toBe('e1')
  })
  it('colours an advance (negative balance) as paid, never as an error', () => {
    const w = mountIt([entry({ running_balance: '-2000.00', direction: 'CREDIT' })])
    const bal = w.findAll('tbody tr')[0]!.findAll('td')[4]!
    expect(bal.classes()).toContain('text-paid-text')
  })
})

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
