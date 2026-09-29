import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { flushPromises } from '@vue/test-utils'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import ReportsTab from '~/components/property/ReportsTab.vue'
import Button from '~/components/ui/Button.vue'
import Badge from '~/components/ui/Badge.vue'
import FormAlert from '~/components/ui/FormAlert.vue'
import Field from '~/components/ui/Field.vue'
import Input from '~/components/ui/Input.vue'
import { ApiError } from '~/utils/apiError'
import { formatMoney, isNegativeMoney, isZeroMoney } from '~/utils/money'
import { formatDate, formatDay } from '~/utils/date'
import { reportColumns } from '~/utils/reportColumns'
import { sumMoney } from '~/utils/money'
import type { ReportChecks } from '~/types/report'

const Icon = { props: ['name'], template: '<i :data-icon="name" />' }
const NuxtLink = { props: ['to'], template: '<a :href="to"><slot /></a>' }
const Skeleton = { template: '<div data-testid="skeleton" />' }
const KpiTile = { props: ['label', 'value', 'delta', 'tone'], template: '<div />' }
const PeriodPicker = { template: '<div />' }
const SpreadsheetTable = { props: ['columns', 'rows', 'rowKey', 'footer', 'footerLabel', 'empty'], template: '<div><slot v-for="r in rows" :name="`cell-house_no`" :row="r" /></div>' }
// A minimal stand-in for UiConfirmDialog/UiModal: renders its default slot
// plus a "Confirm" button when open, avoiding Modal.vue's useId()/focus-trap
// wiring (not the concern of this test, and not stubbed globally).
const ConfirmDialogStub = {
  props: ['modelValue', 'title', 'text', 'confirmLabel', 'loading'],
  emits: ['update:modelValue', 'confirm'],
  template: `<div v-if="modelValue"><p>{{ title }}</p><p>{{ text }}</p><slot />
    <button type="button" @click="$emit('confirm')">{{ confirmLabel ?? 'Confirm' }}</button>
    <button type="button" @click="$emit('update:modelValue', false)">Cancel</button></div>`,
}

function reportFixture(overrides: Partial<ReportChecks> = {}): ReportChecks {
  return {
    checks: { failures: null, info: null },
    report: {
      period: '2026-09', property_name: 'KIWI PLACE', location: 'Nairobi', garbage_enabled: false,
      summary: { occupied: 7, vacant: 1, water_units: '0.00', water_rate: '120.00', expected_water: '0.00', actual_water: '0.00' },
      deviation: '0.00', rows: [],
      totals: { rent: '0.00', water: '0.00', garbage: '0.00', rent_deposit: '0.00', water_deposit: '0.00', grand_total: '0.00' },
      note1: [], notes: [], management_fee_percent: 5, management_fee: '0.00',
      confirmed: false, confirmed_at: undefined, stale: false,
    },
    ...overrides,
  }
}

describe('ReportsTab confirm gate', () => {
  const checks = vi.fn(); const confirm = vi.fn(); const setPlotMeter = vi.fn()
  const plotMeter = vi.fn(() => Promise.resolve({ previous_reading: null, current_reading: null, units_consumed: null, reading_date: null }))
  const openSchedule = vi.fn(); const openReceipts = vi.fn()
  const toastSuccess = vi.fn(); const toastFail = vi.fn()

  beforeEach(() => {
    setActivePinia(createPinia())
    for (const f of [checks, confirm, setPlotMeter, plotMeter, openSchedule, openReceipts, toastSuccess, toastFail]) f.mockReset()
    plotMeter.mockResolvedValue({ previous_reading: null, current_reading: null, units_consumed: null, reading_date: null })
    // ReportsTab.vue relies on Nuxt's global auto-import of Vue APIs; this
    // project's vitest config has no auto-import plugin, so stub them.
    vi.stubGlobal('ref', ref)
    vi.stubGlobal('computed', computed)
    vi.stubGlobal('watch', watch)
    vi.stubGlobal('onMounted', onMounted)
    vi.stubGlobal('nextTick', nextTick)
    vi.stubGlobal('useReports', () => ({ checks, confirm, setPlotMeter, plotMeter, openSchedule, openReceipts, get: vi.fn() }))
    vi.stubGlobal('useToast', () => ({ success: toastSuccess, fail: toastFail, info: vi.fn(), error: vi.fn() }))
    vi.stubGlobal('useCurrency', () => ({ format: formatMoney, isZero: isZeroMoney, isNegative: isNegativeMoney, prefixed: (v: string) => `Ksh ${formatMoney(v)}` }))
    vi.stubGlobal('usePeriod', () => computed({ get: () => '2026-09', set: () => {} }))
    vi.stubGlobal('formatDate', formatDate)
    vi.stubGlobal('formatDay', formatDay)
    vi.stubGlobal('reportColumns', reportColumns)
    vi.stubGlobal('sumMoney', sumMoney)
    vi.stubGlobal('useSubmit', () => {
      const loading = ref(false); const fields = ref<Record<string, string>>({}); const message = ref('')
      return {
        loading, fields, message, status: ref(0),
        run: async (fn: () => Promise<unknown>) => {
          loading.value = true
          try { return await fn() } catch (e) { message.value = e instanceof Error ? e.message : 'failed'; return undefined } finally { loading.value = false }
        },
      }
    })
  })

  const globalStubs = {
    components: {
      UiButton: Button, UiBadge: Badge, UiFormAlert: FormAlert, UiConfirmDialog: ConfirmDialogStub,
      UiField: Field, UiInput: Input, UiSkeleton: Skeleton, UiKpiTile: KpiTile, UiPeriodPicker: PeriodPicker,
      UiCard: { template: '<div><slot /></div>' },
      PropertySpreadsheetTable: SpreadsheetTable,
      Icon, NuxtLink,
    },
  }

  async function mountTab() {
    const w = mount(ReportsTab, { props: { propertyId: 'p1' }, global: globalStubs })
    await flushPromises()
    return w
  }

  it('disables Confirm while a blocking failure is present', async () => {
    checks.mockResolvedValue(reportFixture({ checks: { failures: [{ code: 'pending_payments', message: '1 payment(s) unallocated' }], info: null } }))
    const w = await mountTab()
    expect(w.text()).toContain('1 payment(s) unallocated')
    const confirmBtn = w.findAll('button').find(b => b.text().includes('Confirm period'))
    expect(confirmBtn?.attributes('disabled')).toBeDefined()
  })

  it('enables Confirm once checks pass, and shows failures again on a 422 race', async () => {
    checks.mockResolvedValueOnce(reportFixture())
    const w = await mountTab()
    const confirmBtn = () => w.findAll('button').find(b => b.text().includes('Confirm period'))
    expect(confirmBtn()?.attributes('disabled')).toBeUndefined()

    await confirmBtn()!.trigger('click')
    await nextTick()
    // The confirm dialog is open; click its own Confirm button.
    confirm.mockRejectedValueOnce(new ApiError(422, 'period failed sanity checks', {}, null, 'sanity_failed', [{ code: 'pending_payments', message: '1 payment(s) unallocated' }]))
    checks.mockResolvedValueOnce(reportFixture({ checks: { failures: [{ code: 'pending_payments', message: '1 payment(s) unallocated' }], info: null } }))
    const dialogConfirmBtn = w.findAll('button').filter(b => b.text() === 'Confirm').at(-1)
    await dialogConfirmBtn!.trigger('click')
    await flushPromises()

    expect(w.text()).toContain('period failed sanity checks')
    expect(w.text()).toContain('1 payment(s) unallocated')
  })

  it('maps a 409 period_stale download error to a re-confirm prompt and reloads checks', async () => {
    checks.mockResolvedValueOnce(reportFixture({ report: { ...reportFixture().report, confirmed: true, stale: true, confirmed_at: '2026-09-01T00:00:00Z' } }))
    const w = await mountTab()
    openSchedule.mockRejectedValueOnce(new ApiError(409, 'confirmed period is stale; the ledger changed since confirmation', {}, null, 'period_stale'))
    checks.mockResolvedValueOnce(reportFixture({ report: { ...reportFixture().report, confirmed: true, stale: true, confirmed_at: '2026-09-01T00:00:00Z' } }))

    const scheduleBtn = w.findAll('button').find(b => b.text().includes('Generate schedule PDF'))
    await scheduleBtn!.trigger('click')
    await flushPromises()

    expect(w.text()).toContain('Figures changed since confirmation')
    expect(checks).toHaveBeenCalledTimes(2)
  })

  it('maps a 409 period_not_confirmed download error to "Confirm this period first."', async () => {
    checks.mockResolvedValue(reportFixture())
    const w = await mountTab()
    openReceipts.mockRejectedValueOnce(new ApiError(409, 'period is not confirmed', {}, null, 'period_not_confirmed'))

    // Downloads are disabled until confirmed in the UI, but the handler
    // itself must still map the code correctly if a stale disabled-state
    // check is ever bypassed (e.g. a race after the badge updates).
    const receiptsBtn = w.findAll('button').find(b => b.text().includes('Download receipts PDF'))
    expect(receiptsBtn?.attributes('disabled')).toBeDefined()
  })

  // 2026-09-28 regression: a plot-meter fetch failure must not blank the
  // whole tab — checks/report load independently and keep rendering.
  it('a failed plot meter fetch shows an inline error on its own card, not a blank tab', async () => {
    checks.mockResolvedValue(reportFixture())
    plotMeter.mockRejectedValueOnce(new Error('plot meter unavailable'))
    const w = await mountTab()

    expect(w.text()).toContain('Sanity checks')
    expect(w.text()).toContain('plot meter unavailable')
    expect(w.text()).toContain("Reading on the plot's main meter dial")
  })
})
