<script setup lang="ts">
import type { ElectricityDepositRow, ElectricityDeposits } from '~/types/billing'

const props = defineProps<{ propertyId: string }>()

const ledgers = useLedgers()
const toast = useToast()
const cur = useCurrency()

const deposits = ref<ElectricityDeposits | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try { deposits.value = await ledgers.electricityDeposits(props.propertyId) }
  catch (e) { error.value = e instanceof Error ? e.message : 'Could not load electricity deposits.' } finally { loading.value = false }
}
onMounted(load)
watch(() => props.propertyId, load)

const rows = computed(() => deposits.value?.rows ?? [])
const columns = [
  { key: 'unit_code', label: 'House no.' }, { key: 'tenant_name', label: 'Tenant' },
  { key: 'deposit_required', label: 'Deposit required', align: 'right' as const },
  { key: 'paid', label: 'Paid', align: 'right' as const },
  { key: 'balance', label: 'Balance', align: 'right' as const },
]
const footer = computed(() => deposits.value ? ({
  deposit_required: cur.format(deposits.value.total_required),
  paid: cur.format(deposits.value.total_paid),
  balance: cur.format(deposits.value.total_balance),
}) : undefined)

async function downloadCSV() {
  try { await ledgers.downloadElectricityDepositsCSV(props.propertyId) } catch (e) { toast.fail(e) }
}
async function downloadPDF() {
  try { await ledgers.openElectricityDepositsPDF(props.propertyId) } catch (e) { toast.fail(e) }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="sr-only">Electricity deposits</h2>
      <div class="flex gap-3">
        <UiButton variant="secondary" :disabled="!rows.length" @click="downloadCSV"><Icon name="lucide:file-spreadsheet" class="size-4" />Download CSV</UiButton>
        <UiButton variant="secondary" :disabled="!rows.length" @click="downloadPDF"><Icon name="lucide:file-text" class="size-4" />Download PDF</UiButton>
      </div>
    </div>

    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading" block />

    <template v-else-if="deposits">
      <UiFormAlert v-if="!deposits.enabled" tone="info">Electricity deposit is switched off for this property. Turn it on in <NuxtLink :to="{ query: { tab: 'settings' } }" class="font-semibold text-accent-text">Settings</NuxtLink>.</UiFormAlert>
      <PropertySpreadsheetTable :columns="columns" :rows="rows" :row-key="(r: ElectricityDepositRow) => r.unit_id" :footer="rows.length ? footer : undefined" empty="No occupied units.">
        <template #cell-unit_code="{ row }"><NuxtLink :to="`/properties/${propertyId}/units/${row.unit_id}`" class="tnum font-semibold text-accent-text">{{ row.unit_code }}</NuxtLink></template>
        <template #cell-deposit_required="{ row }"><span v-if="cur.isZero(row.deposit_required)" class="text-gray-500 italic">Not billed</span><template v-else>{{ cur.format(row.deposit_required) }}</template></template>
        <template #cell-balance="{ row }"><span :class="cur.isZero(row.balance) ? 'text-paid-text' : 'font-semibold text-arrears-text'">{{ cur.format(row.balance) }}</span></template>
      </PropertySpreadsheetTable>
    </template>
  </div>
</template>
