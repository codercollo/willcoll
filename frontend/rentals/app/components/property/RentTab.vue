<script setup lang="ts">
import type { BadgeStatus } from '~/components/ui/Badge.vue'
import type { RentOverviewRow } from '~/types/billing'
import { isValidMoneyInput } from '~/utils/money'

const props = defineProps<{ propertyId: string }>()
const emit = defineEmits<{ changed: [] }>()

const ledgers = useLedgers()
const toast = useToast()
const cur = useCurrency()
const period = usePeriod()

const rows = ref<RentOverviewRow[]>([])
const loading = ref(true)
const error = ref('')
const edits = reactive<Record<string, string>>({})

async function load() {
  loading.value = true
  error.value = ''
  try {
    rows.value = await ledgers.rentOverview(props.propertyId, period.value)
    for (const k of Object.keys(edits)) delete edits[k]
  } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load rent.' } finally { loading.value = false }
}
onMounted(load)
watch([period, () => props.propertyId], load)

// A billed month is a snapshot: only unbilled rows can take a new expected rent.
const editable = (r: RentOverviewRow) => !r.billed
const changed = (r: RentOverviewRow) => edits[r.unit_id] !== undefined && edits[r.unit_id] !== r.expected
const invalid = (r: RentOverviewRow) => changed(r) && (!isValidMoneyInput(edits[r.unit_id]!) || Number(edits[r.unit_id]) <= 0)
const dirty = computed(() => rows.value.filter(changed))
const anyInvalid = computed(() => rows.value.some(invalid))

const columns = [
  { key: 'unit_code', label: 'House no.' }, { key: 'tenant_name', label: 'Tenant' },
  { key: 'expected', label: 'Expected', align: 'right' as const }, { key: 'paid_this_period', label: 'Paid', align: 'right' as const },
  { key: 'balance', label: 'Balance', align: 'right' as const }, { key: 'status', label: 'Status' },
]
const footer = computed(() => ({
  expected: cur.format(sumMoney(rows.value.map(r => r.expected))),
  paid_this_period: cur.format(sumMoney(rows.value.map(r => r.paid_this_period))),
  balance: cur.format(sumMoney(rows.value.map(r => r.balance))),
}))

const statusBadge: Record<string, { status: BadgeStatus; label: string }> = {
  paid: { status: 'paid', label: 'Paid' }, partial: { status: 'partial', label: 'Partial' }, arrears: { status: 'arrears', label: 'Arrears' },
}
const balanceClass = (r: RentOverviewRow) => (cur.isNegative(r.balance) ? 'text-paid-text' : cur.isZero(r.balance) ? 'text-paid-text' : 'text-arrears-text')

const save = useSubmit()
async function saveSchedule() {
  if (anyInvalid.value || !dirty.value.length) return
  const ok = await save.run(async () => { await ledgers.saveRentSchedule(props.propertyId, dirty.value.map(r => ({ unit_id: r.unit_id, rent_amount: edits[r.unit_id]! }))); return true })
  if (ok) { toast.success('Rent amounts saved. They apply to the next rent run.'); await load(); emit('changed') }
}

const confirm = ref(false)
const gen = useSubmit()
const unbilled = computed(() => rows.value.filter(r => !r.billed).length)
async function generate() {
  const run = await gen.run(() => ledgers.generateRent(props.propertyId, period.value))
  confirm.value = false
  if (!run) return
  if (run.billed === 0) toast.info(`Nothing new to bill: all ${run.skipped} units were already billed.`)
  else toast.success(`Billed ${run.billed} units, Ksh ${cur.format(run.total_amount)}${run.skipped ? ` (${run.skipped} already billed)` : ''}.`)
  await load()
  emit('changed')
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <UiPeriodPicker v-model="period" />
      <div class="flex gap-3">
        <UiButton v-if="dirty.length" variant="secondary" :loading="save.loading.value" :disabled="anyInvalid" @click="saveSchedule">Save {{ dirty.length }} rent change{{ dirty.length > 1 ? 's' : '' }}</UiButton>
        <UiButton :disabled="!unbilled || loading" @click="confirm = true"><Icon name="lucide:receipt" class="size-4" />Generate rent invoices</UiButton>
      </div>
    </div>

    <UiFormAlert v-if="save.message.value">{{ save.message.value }}</UiFormAlert>
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading" block />

    <template v-else-if="!error">
      <PropertySpreadsheetTable :columns="columns" :rows="rows" :row-key="(r: RentOverviewRow) => r.unit_id" :footer="rows.length ? footer : undefined" empty="No occupied units with an active lease.">
        <template #cell-unit_code="{ row }"><NuxtLink :to="`/properties/${propertyId}/units/${row.unit_id}`" class="tnum font-semibold text-accent-text">{{ row.unit_code }}</NuxtLink></template>
        <template #cell-expected="{ row }">
          <PropertySpreadsheetInput v-if="editable(row)" :model-value="edits[row.unit_id] ?? row.expected" :label="`Expected rent for ${row.unit_code}`" :changed="changed(row)" :invalid="invalid(row)" @update:model-value="(v) => (edits[row.unit_id] = v)" />
          <template v-else>{{ cur.format(row.expected) }}</template>
        </template>
        <template #cell-paid_this_period="{ row }">{{ cur.format(row.paid_this_period) }}</template>
        <template #cell-balance="{ row }"><span v-if="!row.billed" class="text-gray-500">—</span><span v-else :class="['font-semibold', balanceClass(row)]">{{ cur.format(cur.isNegative(row.balance) ? row.balance.replace('-', '') : row.balance) }}<span v-if="cur.isNegative(row.balance)" class="ml-1 font-normal"> advance</span></span></template>
        <template #cell-status="{ row }"><UiBadge v-if="!row.billed" status="pending" label="Not billed" />
          <UiBadge v-else :status="statusBadge[row.status]!.status" :label="cur.isNegative(row.balance) ? 'Advance' : statusBadge[row.status]!.label" /></template>
      </PropertySpreadsheetTable>
      <p class="text-gray-500">Rent amounts can be changed for months not yet billed. A change applies to the next run, never to a month already billed.</p>
    </template>

    <UiConfirmDialog v-model="confirm" title="Generate rent invoices?" :text="`${unbilled} units will be billed for ${periodLabel(period)}. Units already billed are skipped, so this is safe to repeat.`" confirm-label="Generate" :loading="gen.loading.value" @confirm="generate">
      <UiFormAlert v-if="gen.message.value" class="mt-4">{{ gen.message.value }}</UiFormAlert>
    </UiConfirmDialog>
  </div>
</template>
