<script setup lang="ts">
import type { PlotMeterReading, ReportChecks, ReportPayment, ReportRow } from '~/types/report'
import type { BadgeStatus } from '~/components/ui/Badge.vue'
import { ApiError } from '~/utils/apiError'

// Reports tab (design-tokens 7.9): the on-screen preview has the exact columns
// of the printed ALL IN ONE PAYMENTS SCHEDULE, so there are no surprises
// between what the manager reviews and what the landlord is handed.
const props = defineProps<{ propertyId: string }>()

const reports = useReports()
const toast = useToast()
const cur = useCurrency()
const period = usePeriod()

const data = ref<ReportChecks | null>(null)
const loading = ref(true)
const error = ref('')

// The plot meter card loads independently of the checks/report card: a
// failure there (e.g. the property has never been read, or a transient
// error) must not blank the rest of the tab.
const meter = ref<PlotMeterReading | null>(null)
const meterLoading = ref(true)
const meterError = ref('')

async function loadChecks() {
  loading.value = true
  error.value = ''
  try {
    data.value = await reports.checks(props.propertyId, period.value)
  } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load the report.' } finally { loading.value = false }
}

async function loadMeter() {
  meterLoading.value = true
  meterError.value = ''
  try {
    meter.value = await reports.plotMeter(props.propertyId, period.value)
  } catch (e) { meterError.value = e instanceof Error ? e.message : 'Could not load the plot meter reading.' } finally { meterLoading.value = false }
}

function load() {
  return Promise.allSettled([loadChecks(), loadMeter()])
}
onMounted(load)
watch([period, () => props.propertyId], load)

const report = computed(() => data.value?.report ?? null)
const failures = computed(() => data.value?.checks.failures ?? [])
const info = computed(() => data.value?.checks.info ?? [])

type Status = 'draft' | 'needs_review' | 'confirmed' | 'stale'
const status = computed<Status>(() => {
  const r = report.value
  if (!r) return 'draft'
  if (r.confirmed) return r.stale ? 'stale' : 'confirmed'
  return failures.value.length ? 'needs_review' : 'draft'
})
const statusBadge: Record<Status, { status: BadgeStatus; label: string }> = {
  draft: { status: 'pending', label: 'Draft' },
  needs_review: { status: 'review', label: 'Needs review' },
  confirmed: { status: 'paid', label: 'Confirmed' },
  stale: { status: 'arrears', label: 'Stale' },
}

// A "fix" link for the failure codes that point somewhere specific; the
// rest (row/column totals mismatches) are data-integrity errors with no
// single unit to jump to.
const fixLink: Record<string, { label: string; to: string }> = {
  water_billed_mismatch: { label: 'Review water readings', to: `/properties/${props.propertyId}?tab=water` },
  pending_payments: { label: 'Review payment queue', to: '/payments' },
}

const garbage = computed(() => report.value?.garbage_enabled === true)
const electricityDeposit = computed(() => report.value?.electricity_deposit_shown === true)
const columns = computed(() => reportColumns(garbage.value, electricityDeposit.value))

type PayKey = 'rent' | 'water' | 'garbage' | 'rent_deposit' | 'water_deposit' | 'electricity_deposit'
const rowTotal = (r: ReportRow) => sumMoney((['rent', 'water', 'garbage', 'rent_deposit', 'water_deposit', 'electricity_deposit'] as PayKey[])
  .filter(k => (k !== 'garbage' || garbage.value) && (k !== 'electricity_deposit' || electricityDeposit.value))
  .flatMap(k => (r[k] ?? []).map((p: ReportPayment) => p.amount)))

const footer = computed(() => {
  const t = report.value?.totals
  if (!t) return undefined
  return {
    rent: cur.format(t.rent), water: cur.format(t.water), garbage: cur.format(t.garbage),
    rent_deposit: cur.format(t.rent_deposit), water_deposit: cur.format(t.water_deposit),
    electricity_deposit: cur.format(t.electricity_deposit), total: cur.format(t.grand_total),
  }
})

const confirmedAtLabel = computed(() => report.value?.confirmed ? formatDate(report.value.confirmed_at ?? '') : '')

const monthLabel = computed(() => {
  const [y, m] = period.value.split('-').map(Number)
  return new Date(Date.UTC(y ?? 2026, (m ?? 1) - 1, 1)).toLocaleDateString('en-KE', { month: 'long', year: 'numeric', timeZone: 'UTC' })
})

// ---- confirm ---------------------------------------------------------------
const confirmDialogOpen = ref(false)
const confirming = ref(false)
const confirmError = ref('')
async function doConfirm() {
  confirming.value = true
  confirmError.value = ''
  try {
    await reports.confirm(props.propertyId, period.value)
    confirmDialogOpen.value = false
    toast.success(`${monthLabel.value} confirmed. The schedule and statement are ready to download.`)
    await load()
  } catch (e) {
    // A race (the ledger changed between load and click) re-fails the same
    // sanity check Checks already ran; reload so the panel shows it live.
    confirmError.value = e instanceof Error ? e.message : 'Could not confirm this period.'
    if (e instanceof ApiError && e.code === 'sanity_failed') await load()
  } finally {
    confirming.value = false
  }
}

// ---- plot meter -------------------------------------------------------------
// First reading ever for the property (no previous to default from): the
// previous dial value has to be typed in by hand, once.
const hasPreviousOnFile = computed(() => meter.value?.previous_reading != null)
const previousInput = ref('')
const currentInput = ref('')
watch(meter, (m) => {
  previousInput.value = m?.previous_reading ?? ''
  currentInput.value = m?.current_reading ?? ''
}, { immediate: true })

const unitsUsedPreview = computed(() => {
  const prev = hasPreviousOnFile.value ? meter.value?.previous_reading : previousInput.value
  if (!prev || !currentInput.value) return null
  const used = Number(currentInput.value) - Number(prev)
  return Number.isFinite(used) ? used : null
})

const savingMeter = useSubmit()
async function saveMeter() {
  const previousOverride = hasPreviousOnFile.value ? undefined : previousInput.value
  const r = await savingMeter.run(() => reports.setPlotMeter(props.propertyId, period.value, currentInput.value, previousOverride))
  if (r !== undefined) { toast.success('Plot meter reading saved.'); await load() }
}

// ---- downloads --------------------------------------------------------------
const sanityPanel = ref<{ $el: HTMLElement } | null>(null)
const busy = ref('')
const downloadError = ref('')
const downloadErrorCode = ref<string | null>(null)
async function open(kind: 'schedule' | 'receipts') {
  busy.value = kind
  downloadError.value = ''
  downloadErrorCode.value = null
  try {
    if (kind === 'schedule') await reports.openSchedule(props.propertyId, period.value)
    else await reports.openReceipts(props.propertyId, period.value)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'period_not_confirmed') {
      downloadError.value = 'Confirm this period first.'
      downloadErrorCode.value = e.code
    } else if (e instanceof ApiError && e.code === 'period_stale') {
      downloadError.value = 'Figures changed since confirmation. Review and re-confirm.'
      downloadErrorCode.value = e.code
      await load()
      sanityPanel.value?.$el?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    } else if (e instanceof ApiError && e.code === 'payment_particulars_missing') {
      downloadError.value = e.message
      downloadErrorCode.value = e.code
    } else {
      toast.fail(e)
    }
  } finally { busy.value = '' }
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex flex-wrap items-center gap-3">
        <UiPeriodPicker v-model="period" />
        <UiBadge v-if="report" :status="statusBadge[status].status" :label="statusBadge[status].label" />
      </div>
      <div class="flex flex-wrap gap-3">
        <UiButton variant="secondary" :loading="busy === 'receipts'" :disabled="loading || !!error || !report?.confirmed" @click="open('receipts')"><Icon name="lucide:receipt-text" class="size-4" />Download receipts PDF</UiButton>
        <UiButton :loading="busy === 'schedule'" :disabled="loading || !!error || !report?.confirmed" @click="open('schedule')"><Icon name="lucide:file-spreadsheet" class="size-4" />Generate schedule PDF</UiButton>
      </div>
    </div>

    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiFormAlert v-if="downloadError">
      {{ downloadError }}
      <NuxtLink v-if="downloadErrorCode === 'payment_particulars_missing'" :to="`/properties/${propertyId}?tab=settings`" class="font-semibold text-accent-text">Go to property settings</NuxtLink>
    </UiFormAlert>
    <UiSkeleton v-if="loading" block />

    <template v-else-if="report">
      <UiCard ref="sanityPanel">
        <h2 class="mb-4">Sanity checks</h2>
        <div v-if="!failures.length && !info.length" class="flex items-center gap-2 text-paid-text">
          <Icon name="lucide:circle-check" class="size-4" />No issues found this period.
        </div>
        <ul v-if="failures.length" class="mb-3 flex flex-col gap-2">
          <li v-for="f in failures" :key="f.code" class="flex items-start gap-2 text-arrears-text">
            <Icon name="lucide:circle-alert" class="mt-0.5 size-4 shrink-0" />
            <span>
              {{ f.message }}
              <NuxtLink v-if="fixLink[f.code]" :to="fixLink[f.code]!.to" class="ml-1 font-semibold text-accent-text">{{ fixLink[f.code]!.label }}</NuxtLink>
            </span>
          </li>
        </ul>
        <ul v-if="info.length" class="flex flex-col gap-1">
          <li v-for="(i, idx) in info" :key="idx" class="flex items-start gap-2 text-gray-500">
            <Icon name="lucide:info" class="mt-0.5 size-4 shrink-0" />{{ i }}
          </li>
        </ul>

        <div v-if="report.note1?.length || report.notes.length" class="mt-6 border-t border-gray-200 pt-4">
          <p class="mb-2 text-[length:var(--text-label)] font-semibold text-gray-900">NOTE preview — printed exactly as shown, frozen at confirmation</p>
          <p v-for="(l, i) in report.note1 ?? []" :key="`n1-${i}`" class="text-[length:var(--text-caption)] text-gray-700">{{ l }}</p>
          <p v-for="(l, i) in report.notes" :key="`n2-${i}`" class="mt-1 text-[length:var(--text-caption)] text-gray-700">{{ l }}</p>
        </div>

        <div class="mt-6 border-t border-gray-200 pt-4">
          <p class="mb-2 text-[length:var(--text-label)] font-semibold text-gray-900">Reading on the plot's main meter dial</p>
          <UiSkeleton v-if="meterLoading" />
          <UiFormAlert v-else-if="meterError">{{ meterError }} <button type="button" class="font-semibold text-accent-text" @click="loadMeter">Retry</button></UiFormAlert>
          <template v-else>
            <div class="flex flex-wrap items-end gap-3">
              <UiField v-if="hasPreviousOnFile" label="Previous" class="w-40">
                <UiInput :model-value="meter?.previous_reading ?? ''" prefix="units" disabled />
              </UiField>
              <UiField v-else label="Previous (first reading — type the dial's last value)" for="plot-meter-previous" class="w-64">
                <UiInput id="plot-meter-previous" v-model="previousInput" prefix="units" :invalid="!!savingMeter.fields.value.previous_reading" />
              </UiField>
              <UiField label="Current" for="plot-meter-current" class="w-40">
                <UiInput id="plot-meter-current" v-model="currentInput" prefix="units" :invalid="!!savingMeter.fields.value.current_reading" />
              </UiField>
              <p class="pb-2.5 text-[length:var(--text-body)] text-gray-700">
                = <span class="font-semibold tnum">{{ unitsUsedPreview ?? '—' }}</span> units used
              </p>
              <UiButton variant="secondary" :loading="savingMeter.loading.value" :disabled="!currentInput || (!hasPreviousOnFile && !previousInput)" @click="saveMeter">Save reading</UiButton>
            </div>
            <p v-if="savingMeter.message.value" class="mt-2 text-[length:var(--text-caption)] text-arrears-text">{{ savingMeter.message.value }}</p>
          </template>
        </div>

        <div class="mt-6 flex flex-col gap-2 border-t border-gray-200 pt-4">
          <UiFormAlert v-if="confirmError">{{ confirmError }}</UiFormAlert>
          <div class="flex items-center gap-3">
            <UiButton :disabled="!!failures.length || status === 'confirmed'" @click="confirmDialogOpen = true">
              {{ status === 'confirmed' ? 'Confirmed' : status === 'stale' ? 'Re-confirm' : 'Confirm period' }}
            </UiButton>
            <p v-if="report.confirmed" class="text-[length:var(--text-caption)] text-gray-500">Last confirmed {{ confirmedAtLabel }}</p>
          </div>
        </div>
      </UiCard>

      <section class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-label="Summary">
        <UiKpiTile label="Units occupied / vacant" :value="`${report.summary.occupied} / ${report.summary.vacant}`" />
        <UiKpiTile label="Water units consumed" :value="cur.format(report.summary.water_units)" :delta="`at Ksh ${cur.format(report.summary.water_rate)} per unit`" />
        <UiKpiTile label="Water expected / actual" :value="`${cur.format(report.summary.expected_water)} / ${cur.format(report.summary.actual_water)}`" :delta="cur.isZero(report.deviation) ? 'No deviation' : `Deviation ${cur.format(report.deviation)}`" :tone="cur.isZero(report.deviation) ? 'paid' : 'arrears'" />
        <UiKpiTile label="Grand total collected" :value="`Ksh ${cur.format(report.totals.grand_total)}`" :delta="`Management fee ${report.management_fee_percent}% = Ksh ${cur.format(report.management_fee)}`" />
      </section>

      <PropertySpreadsheetTable :columns="columns" :rows="report.rows" :row-key="(r: ReportRow) => r.house_no" :footer="report.rows.length ? footer : undefined" footer-label="Totals" empty="No occupied units this month.">
        <template #cell-house_no="{ row }"><span class="tnum font-semibold">{{ row.house_no }}</span></template>
        <template #cell-tenants="{ row }">
          <div>
            <p class="font-semibold">{{ row.tenants?.[0] ?? '—' }}</p>
            <p v-for="t in (row.tenants ?? []).slice(1)" :key="t" class="text-[length:var(--text-caption)] text-gray-500">OR {{ t }}</p>
          </div>
        </template>
        <template v-for="k in (['rent', 'water', 'garbage', 'rent_deposit', 'water_deposit', 'electricity_deposit'] as const)" :key="k" #[`cell-${k}`]="{ row }">
          <!-- Several payments to one ledger stack as several items in one cell. -->
          <div v-if="row[k]?.length" class="flex flex-col items-end">
            <span v-for="(p, i) in row[k]" :key="i" class="money">{{ cur.format(p.amount) }} <span class="text-[length:var(--text-caption)] font-normal text-gray-500">{{ formatDay(p.date) }}</span></span>
          </div>
          <span v-else class="text-gray-500">—</span>
        </template>
        <template #cell-total="{ row }"><span class="font-bold">{{ cur.format(rowTotal(row)) }}</span></template>
      </PropertySpreadsheetTable>
    </template>

    <UiConfirmDialog
      v-model="confirmDialogOpen"
      :title="`Confirm ${monthLabel}`"
      :text="`This freezes the notes for ${monthLabel}. Once confirmed, the schedule and receipts can be downloaded; a later ledger change will mark it stale until re-confirmed.`"
      confirm-label="Confirm"
      :loading="confirming"
      @confirm="doConfirm"
    />
  </div>
</template>
