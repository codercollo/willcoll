<script setup lang="ts">
import type { Unit } from '~/types/api'
import type { LedgerType } from '~/types/billing'
import type { ReviewPayment } from '~/types/payments'
import { cmpMoney, isValidMoneyInput, subMoney, sumMoney } from '~/utils/money'

// Place an unallocated payment on one unit, split across its ledgers. The
// split must add up to EXACTLY the payment (the API refuses anything else).
const open = defineModel<boolean>({ default: false })
const props = defineProps<{ payment: ReviewPayment | null }>()
const emit = defineEmits<{ placed: [] }>()

const properties = useProperties()
const units = useUnits()
const ledgers = useLedgers()
const payments = usePayments()
const toast = useToast()
const cur = useCurrency()

const propertyId = ref('')
const unitList = ref<Unit[]>([])
const unitId = ref('')
const owed = ref<Partial<Record<LedgerType, string>>>({})
const amounts = reactive<Record<string, string>>({})
const loadingUnits = ref(false)
const loadingOwed = ref(false)
const { loading, message, status, run } = useSubmit()

const ledgerLabels: Record<LedgerType, string> = { rent: 'Rent', water: 'Water', garbage: 'Garbage', rent_deposit: 'Rent deposit', water_deposit: 'Water deposit', electricity_deposit: 'Electricity deposit' }
const property = computed(() => properties.list.find(p => p.id === propertyId.value))
const types = computed<LedgerType[]>(() => (['rent', 'water', 'garbage', 'rent_deposit', 'water_deposit', 'electricity_deposit'] as LedgerType[])
  .filter(t => (t !== 'garbage' || property.value?.garbage_enabled) && (t !== 'electricity_deposit' || property.value?.electricity_enabled)))

watch(open, async (v) => {
  if (!v || !props.payment) return
  message.value = ''
  propertyId.value = ''; unitId.value = ''; unitList.value = []; owed.value = {}
  for (const k of Object.keys(amounts)) delete amounts[k]
  try {
    if (!properties.list.length) await properties.fetchList(currentPeriod())
    // A payment the engine narrowed to one unit starts on that unit.
    if (props.payment.matched_unit_id) {
      const u = await units.get(props.payment.matched_unit_id)
      propertyId.value = u.property_id
      await loadUnits()
      unitId.value = u.id
    } else if (properties.list.length === 1) propertyId.value = properties.list[0]!.id
  } catch (e) { toast.fail(e) }
})

async function loadUnits() {
  if (!propertyId.value) return
  loadingUnits.value = true
  try { unitList.value = (await units.all(propertyId.value)).filter(u => u.status === 'occupied') } catch (e) { toast.fail(e) } finally { loadingUnits.value = false }
}
watch(propertyId, () => { unitId.value = ''; unitList.value = []; loadUnits() })

watch(unitId, async (id) => {
  owed.value = {}
  for (const k of Object.keys(amounts)) delete amounts[k]
  if (!id) return
  loadingOwed.value = true
  try { owed.value = await ledgers.balances(id, types.value) } catch (e) { toast.fail(e) } finally { loadingOwed.value = false }
})

const entered = computed(() => types.value.filter(t => (amounts[t] ?? '') !== ''))
const invalid = (t: LedgerType) => (amounts[t] ?? '') !== '' && (!isValidMoneyInput(amounts[t]!) || cmpMoney(amounts[t], '0') <= 0)
const anyInvalid = computed(() => types.value.some(invalid))
const total = computed(() => sumMoney(entered.value.map(t => amounts[t])))
const remaining = computed(() => subMoney(props.payment?.amount, total.value))
const balanced = computed(() => !!unitId.value && entered.value.length > 0 && !anyInvalid.value && cmpMoney(remaining.value, '0') === 0)

// Suggest what is owed on a ledger, capped at what is left of the payment.
function fill(t: LedgerType) {
  const debt = owed.value[t]
  if (!debt || cmpMoney(debt, '0') <= 0) return
  const left = subMoney(remaining.value, amounts[t] ?? '0')
  amounts[t] = cmpMoney(debt, left) < 0 ? debt : left
}

async function place() {
  if (!props.payment || !balanced.value) return
  const ok = await run(async () => { await payments.allocate(props.payment!.id, unitId.value, entered.value.map(t => ({ ledger_type: t, amount: amounts[t]! }))); return true })
  if (ok) { toast.success('Payment placed on the unit.'); open.value = false; emit('placed') }
  else if (status.value === 409) { toast.error(message.value); open.value = false; emit('placed') }
}
</script>

<template>
  <UiModal v-model="open" title="Assign payment to a unit" width="lg">
    <div v-if="payment" class="flex flex-col gap-5">
      <div class="rounded-sm bg-gray-50 p-4">
        <p class="money text-[length:var(--text-money-lg)] font-bold">Ksh {{ cur.format(payment.amount) }}</p>
        <p class="text-gray-700">{{ payment.payer_name || 'Unknown payer' }} &middot; <span class="tnum">{{ payment.msisdn }}</span> &middot; {{ payment.mpesa_receipt }}</p>
        <p v-if="payment.review_note" class="mt-1 text-gray-500">{{ payment.review_note }}</p>
      </div>

      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>

      <div class="grid gap-5 sm:grid-cols-2">
        <UiField label="Property" for="al-prop"><UiSelect id="al-prop" v-model="propertyId" placeholder="Choose a property" :options="properties.list.map(p => ({ value: p.id, label: p.name }))" /></UiField>
        <UiField label="Unit" for="al-unit" :help="loadingUnits ? 'Loading units…' : undefined">
          <UiSelect id="al-unit" v-model="unitId" :disabled="!propertyId || loadingUnits" placeholder="Choose a unit" :options="unitList.map(u => ({ value: u.id, label: `${u.unit_code}${u.current_lease ? ' - ' + u.current_lease.tenant_name : ''}` }))" />
        </UiField>
      </div>

      <template v-if="unitId">
        <UiSkeleton v-if="loadingOwed" :lines="3" />
        <div v-else class="overflow-hidden rounded-sm border border-border-default">
          <table class="w-full text-left">
            <thead><tr class="h-10 bg-gray-50"><th class="th-text px-4 font-medium">Ledger</th><th class="th-text px-4 text-right font-medium">Owes</th><th class="th-text px-4 text-right font-medium">Apply</th><th class="w-16" /></tr></thead>
            <tbody>
              <tr v-for="t in types" :key="t" class="h-12 border-t border-border-subtle">
                <td class="px-4 font-semibold">{{ ledgerLabels[t] }}</td>
                <td :class="['money px-4 text-right', cmpMoney(owed[t], '0') > 0 ? 'text-arrears-text' : 'text-paid-text']">{{ cmpMoney(owed[t], '0') < 0 ? `${cur.format(owed[t]?.replace('-', ''))} credit` : cur.format(owed[t]) }}</td>
                <td class="px-4 text-right">
                  <input v-model="amounts[t]" inputmode="decimal" autocomplete="off" :aria-label="`Amount to apply to ${ledgerLabels[t]}`" :aria-invalid="invalid(t) || undefined" placeholder="0.00"
                    :class="['money h-9 w-28 rounded-sm border bg-white px-2 text-right font-semibold outline-none focus:border-accent-primary focus:ring-2 focus:ring-accent-primary-tint', invalid(t) ? 'border-arrears' : 'border-border-default']">
                </td>
                <td class="px-2"><UiButton variant="icon" :label="`Fill ${ledgerLabels[t]} with what is owed`" :disabled="cmpMoney(owed[t], '0') <= 0" @click="fill(t)"><Icon name="lucide:wand-sparkles" class="size-4" /></UiButton></td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="flex items-center justify-between rounded-sm p-4" :class="balanced ? 'bg-paid-tint' : cmpMoney(remaining, '0') < 0 ? 'bg-arrears-tint' : 'bg-gray-50'" role="status">
          <span>{{ balanced ? 'The split matches the payment.' : cmpMoney(remaining, '0') < 0 ? 'You have applied more than the payment.' : 'Left to apply' }}</span>
          <span class="money font-bold" :class="cmpMoney(remaining, '0') < 0 ? 'text-arrears-text' : ''">Ksh {{ cur.format(remaining) }}</span>
        </div>
      </template>
    </div>

    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton :loading="loading" :disabled="!balanced" @click="place">Place payment</UiButton>
    </template>
  </UiModal>
</template>
