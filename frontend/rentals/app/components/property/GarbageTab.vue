<script setup lang="ts">
import type { GarbagePreview, GarbageRow } from '~/types/billing'

const props = defineProps<{ propertyId: string }>()
const emit = defineEmits<{ changed: [] }>()

const ledgers = useLedgers()
const toast = useToast()
const cur = useCurrency()
const period = usePeriod()

const preview = ref<GarbagePreview | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try { preview.value = await ledgers.garbagePreview(props.propertyId, period.value) }
  catch (e) { error.value = e instanceof Error ? e.message : 'Could not load garbage.' } finally { loading.value = false }
}
onMounted(load)
watch([period, () => props.propertyId], load)

const rows = computed(() => preview.value?.rows ?? [])
const columns = [
  { key: 'unit_code', label: 'House no.' }, { key: 'tenant_name', label: 'Tenant' },
  { key: 'fee', label: 'Garbage fee', align: 'right' as const }, { key: 'prior_balance', label: 'Prior bal. B/F', align: 'right' as const },
  { key: 'total_due', label: 'Total due', align: 'right' as const },
]
const footer = computed(() => ({
  fee: cur.format(sumMoney(rows.value.map(r => r.fee))),
  prior_balance: cur.format(sumMoney(rows.value.map(r => r.prior_balance))),
  total_due: cur.format(sumMoney(rows.value.map(r => r.total_due))),
}))

const confirm = ref(false)
const gen = useSubmit()
async function generate() {
  const run = await gen.run(() => ledgers.generateGarbage(props.propertyId, period.value))
  if (!run) { if (gen.status.value === 409) { confirm.value = false; await load() } return }
  confirm.value = false
  toast.success(`Garbage billed for ${periodLabel(period.value)}.`)
  await load()
  emit('changed')
}
async function bills() {
  try { await ledgers.openBills('garbage', props.propertyId, period.value) } catch (e) { toast.fail(e) }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <UiPeriodPicker v-model="period" />
      <div class="flex gap-3">
        <UiButton variant="secondary" :disabled="!preview?.already_generated" @click="bills"><Icon name="lucide:file-text" class="size-4" />Garbage bills PDF</UiButton>
        <UiButton :disabled="loading || !preview?.enabled || preview?.already_generated || !preview?.billed_count" @click="confirm = true"><Icon name="lucide:trash-2" class="size-4" />Generate garbage invoices</UiButton>
      </div>
    </div>

    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading" block />

    <template v-else-if="preview">
      <UiFormAlert v-if="!preview.enabled" tone="info">Garbage collection is switched off for this property. Turn it on in <NuxtLink :to="{ query: { tab: 'settings' } }" class="font-semibold text-accent-text">Settings</NuxtLink>.</UiFormAlert>
      <UiFormAlert v-else-if="preview.already_generated" tone="success">Garbage for {{ periodLabel(period) }} has already been billed.</UiFormAlert>
      <UiFormAlert v-else :tone="preview.billed_count ? 'info' : 'error'">{{ preview.message }}</UiFormAlert>
      <PropertySpreadsheetTable :columns="columns" :rows="rows" :row-key="(r: GarbageRow) => r.unit_id" :locked="() => !!preview?.already_generated" :footer="rows.length ? footer : undefined" empty="No occupied units.">
        <template #cell-unit_code="{ row }"><NuxtLink :to="`/properties/${propertyId}/units/${row.unit_id}`" class="tnum font-semibold text-accent-text">{{ row.unit_code }}</NuxtLink></template>
        <template #cell-prior_balance="{ row }"><span :class="cur.isZero(row.prior_balance) ? 'text-gray-700' : 'font-semibold text-arrears-text'">{{ cur.format(row.prior_balance) }}</span></template>
        <template #cell-fee="{ row }"><span v-if="!row.billed" class="text-gray-500 italic">Not billed</span><template v-else>{{ cur.format(row.fee) }}</template></template>
        <template #cell-total_due="{ row }"><span class="font-bold">{{ row.billed ? cur.format(row.total_due) : cur.format(row.prior_balance) }}</span></template>
      </PropertySpreadsheetTable>
    </template>

    <UiConfirmDialog v-model="confirm" title="Generate garbage invoices?" :text="preview?.message" confirm-label="Generate" :loading="gen.loading.value" @confirm="generate">
      <UiFormAlert v-if="gen.message.value" class="mt-4">{{ gen.message.value }}</UiFormAlert>
    </UiConfirmDialog>
  </div>
</template>
