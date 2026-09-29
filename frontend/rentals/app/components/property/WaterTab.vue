<script setup lang="ts">
import type { WaterGrid, WaterReadingInput, WaterRow } from '~/types/billing'
import { isValidMoneyInput } from '~/utils/money'

const props = defineProps<{ propertyId: string }>()
const emit = defineEmits<{ changed: [] }>()

const ledgers = useLedgers()
const toast = useToast()
const cur = useCurrency()
const period = usePeriod()

const grid = ref<WaterGrid | null>(null)
const loading = ref(true)
const error = ref('')
interface Edit { current?: string; previous?: string; rate?: string }
const edits = reactive<Record<string, Edit>>({})

async function load() {
  loading.value = true
  error.value = ''
  try {
    grid.value = await ledgers.waterGrid(props.propertyId, period.value)
    for (const k of Object.keys(edits)) delete edits[k]
  } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load water.' } finally { loading.value = false }
}
onMounted(load)
watch([period, () => props.propertyId], load)

const rows = computed(() => grid.value?.rows ?? [])
const anyLocked = computed(() => rows.value.some(r => r.locked))
const allLocked = computed(() => rows.value.length > 0 && rows.value.every(r => r.locked))

function shown(r: WaterRow, f: keyof Edit) {
  const e = edits[r.unit_id]?.[f]
  if (e !== undefined) return e
  if (f === 'current') return r.has_reading ? r.current_reading : ''
  if (f === 'previous') return r.previous_reading
  return r.rate
}
function setEdit(r: WaterRow, f: keyof Edit, v: string) { edits[r.unit_id] = { ...edits[r.unit_id], [f]: v } }
function bad(r: WaterRow, f: keyof Edit) {
  const v = edits[r.unit_id]?.[f]
  return v !== undefined && v !== '' && !isValidMoneyInput(v)
}
const isDirty = (r: WaterRow) => !!edits[r.unit_id] && Object.keys(edits[r.unit_id]!).length > 0
const dirtyRows = computed(() => rows.value.filter(isDirty))
const anyBad = computed(() => rows.value.some(r => bad(r, 'current') || bad(r, 'previous') || bad(r, 'rate')))

const columns = [
  { key: 'unit_code', label: 'House no.' }, { key: 'tenant_name', label: 'Tenant' },
  { key: 'previous_reading', label: 'Previous', align: 'right' as const }, { key: 'current_reading', label: 'Current', align: 'right' as const },
  { key: 'units_consumed', label: 'Units', align: 'right' as const }, { key: 'rate', label: 'Rate', align: 'right' as const },
  { key: 'amount', label: 'Amount', align: 'right' as const }, { key: 'prior_balance', label: 'Prior bal. B/F', align: 'right' as const },
  { key: 'total_due', label: 'Total due', align: 'right' as const },
]
// Column totals are sums of what the server returned, never recomputed amounts.
const footer = computed(() => ({
  units_consumed: cur.format(sumMoney(rows.value.map(r => r.units_consumed))),
  amount: cur.format(sumMoney(rows.value.map(r => r.amount))),
  prior_balance: cur.format(sumMoney(rows.value.map(r => r.prior_balance))),
  total_due: cur.format(sumMoney(rows.value.map(r => r.total_due))),
}))

const save = useSubmit()
// Only rows that can actually be saved: a reading must be present (a row the
// user cleared, or never filled, is not a save).
const payload = computed<WaterReadingInput[]>(() => {
  const readings: WaterReadingInput[] = []
  for (const r of dirtyRows.value) {
    const e = edits[r.unit_id]!
    const current = e.current ?? (r.has_reading ? r.current_reading : '')
    if (current === '') continue
    readings.push({ unit_id: r.unit_id, current_reading: current, ...(e.previous !== undefined ? { previous_reading: e.previous } : {}), ...(e.rate !== undefined ? { rate: e.rate } : {}) })
  }
  return readings
})
async function saveReadings() {
  if (anyBad.value) return
  const readings = payload.value
  if (!readings.length) return
  const g = await save.run(() => ledgers.saveWater(props.propertyId, period.value, readings))
  if (g) { grid.value = g; for (const k of Object.keys(edits)) delete edits[k]; toast.success('Readings saved') }
  else if (save.status.value === 409) { toast.error(save.message.value); await load() }
}

// ---- generate ------------------------------------------------------------
const confirm = ref(false)
const gen = useSubmit()
const missing = computed(() => rows.value.filter(r => !r.locked && !r.has_reading).map(r => r.unit_code))
async function generate() {
  const run = await gen.run(() => ledgers.generateWater(props.propertyId, period.value))
  if (!run) { if (gen.status.value === 409) { confirm.value = false; await load() } return }
  confirm.value = false
  toast.success(`Billed ${run.billed} units, Ksh ${cur.format(run.total)}.${run.missing?.length ? ` No reading for: ${run.missing.join(', ')}.` : ''}`)
  await load()
  emit('changed')
}

async function bills() {
  try { await ledgers.openBills('water', props.propertyId, period.value) } catch (e) { toast.fail(e) }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <UiPeriodPicker v-model="period" />
      <div class="flex flex-wrap gap-3">
        <UiButton v-if="payload.length" variant="secondary" :loading="save.loading.value" :disabled="anyBad" @click="saveReadings">Save {{ payload.length }} reading{{ payload.length > 1 ? 's' : '' }}</UiButton>
        <UiButton variant="secondary" :disabled="!anyLocked" @click="bills"><Icon name="lucide:file-text" class="size-4" />Water bills PDF</UiButton>
        <UiButton :disabled="loading || allLocked || !!payload.length" :title="payload.length ? 'Save your readings first' : undefined" @click="confirm = true"><Icon name="lucide:droplets" class="size-4" />Generate billing invoices</UiButton>
      </div>
    </div>

    <UiFormAlert v-if="save.message.value">{{ save.message.value }}</UiFormAlert>
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiFormAlert v-if="allLocked" tone="success">Water for {{ periodLabel(period) }} has been billed. Readings are locked.</UiFormAlert>
    <UiSkeleton v-if="loading" block />

    <template v-else-if="grid">
      <PropertySpreadsheetTable :columns="columns" :rows="rows" :row-key="(r: WaterRow) => r.unit_id" :locked="(r: WaterRow) => r.locked" :footer="rows.length ? footer : undefined" empty="No occupied units to read.">
        <template #cell-unit_code="{ row }"><NuxtLink :to="`/properties/${propertyId}/units/${row.unit_id}`" class="tnum font-semibold text-accent-text">{{ row.unit_code }}</NuxtLink></template>
        <template #cell-previous_reading="{ row }">
          <template v-if="row.locked">{{ cur.format(row.previous_reading) }}</template>
          <PropertySpreadsheetInput v-else :model-value="shown(row, 'previous')" :label="`Previous reading for ${row.unit_code}`" :changed="edits[row.unit_id]?.previous !== undefined" :invalid="bad(row, 'previous')" @update:model-value="(v) => setEdit(row, 'previous', v)" />
        </template>
        <template #cell-current_reading="{ row }">
          <template v-if="row.locked">{{ cur.format(row.current_reading) }}</template>
          <PropertySpreadsheetInput v-else :model-value="shown(row, 'current')" placeholder="—" :label="`Current reading for ${row.unit_code}`" :changed="edits[row.unit_id]?.current !== undefined" :invalid="bad(row, 'current')" @update:model-value="(v) => setEdit(row, 'current', v)" />
        </template>
        <template #cell-units_consumed="{ row }"><span class="text-gray-700">{{ row.has_reading ? cur.format(row.units_consumed) : '—' }}</span></template>
        <template #cell-rate="{ row }">
          <template v-if="row.locked">{{ cur.format(row.rate) }}</template>
          <PropertySpreadsheetInput v-else :model-value="shown(row, 'rate')" :label="`Rate for ${row.unit_code}`" :changed="edits[row.unit_id]?.rate !== undefined" :invalid="bad(row, 'rate')" @update:model-value="(v) => setEdit(row, 'rate', v)" />
        </template>
        <template #cell-amount="{ row }"><span class="text-gray-700">{{ row.has_reading ? cur.format(row.amount) : '—' }}</span></template>
        <template #cell-prior_balance="{ row }"><span :class="cur.isZero(row.prior_balance) ? 'text-gray-700' : 'font-semibold text-arrears-text'">{{ cur.format(row.prior_balance) }}</span></template>
        <template #cell-total_due="{ row }"><span class="font-bold">{{ row.has_reading ? cur.format(row.total_due) : '—' }}</span></template>
      </PropertySpreadsheetTable>
      <p class="text-gray-500">Units, amount and totals are calculated by the system when you save. Default rate: Ksh {{ cur.format(grid.default_rate) }} per unit.</p>
    </template>

    <UiConfirmDialog v-model="confirm" title="Generate water invoices?" :loading="gen.loading.value" confirm-label="Generate and lock" @confirm="generate">
      <p class="text-gray-700">This bills {{ periodLabel(period) }} and <strong>locks every reading</strong>. It cannot be undone; corrections are made with a reversal on the unit ledger.</p>
      <UiFormAlert v-if="missing.length" tone="info" class="mt-4">No reading yet for {{ missing.join(', ') }}. Those units will be skipped.</UiFormAlert>
      <UiFormAlert v-if="gen.message.value" class="mt-4">{{ gen.message.value }}</UiFormAlert>
    </UiConfirmDialog>
  </div>
</template>
