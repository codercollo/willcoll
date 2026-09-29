<script setup lang="ts">
import type { LedgerEntry, LedgerPage, LedgerType } from '~/types/billing'
import { isValidMoneyInput } from '~/utils/money'

// One tab of a unit's ledgers. Rent shows rent AND its deposit, water shows
// water AND its deposit; deposits have no tab of their own (features section 10).
const props = defineProps<{ unitId: string; kind: 'rent' | 'water' | 'garbage' | 'electricity_deposit' }>()
const emit = defineEmits<{ changed: [] }>()

const ledgers = useLedgers()
const toast = useToast()
const cur = useCurrency()

const types = computed<{ key: LedgerType; label: string }[]>(() => {
  if (props.kind === 'rent') return [{ key: 'rent', label: 'Rent' }, { key: 'rent_deposit', label: 'Rent deposit' }]
  if (props.kind === 'water') return [{ key: 'water', label: 'Water' }, { key: 'water_deposit', label: 'Water deposit' }]
  if (props.kind === 'electricity_deposit') return [{ key: 'electricity_deposit', label: 'Electricity deposit' }]
  return [{ key: 'garbage', label: 'Garbage' }]
})
const type = ref<LedgerType>(types.value[0]!.key)
const page = ref(1)
const data = ref<LedgerPage | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try { data.value = await ledgers.ledger(props.unitId, type.value, page.value) }
  catch (e) { error.value = e instanceof Error ? e.message : 'Could not load the ledger.' } finally { loading.value = false }
}
onMounted(load)
watch([type, page, () => props.unitId], load)
watch(type, () => { page.value = 1 })

const balance = computed(() => data.value?.balance.balance ?? '0.00')
const state = computed(() => {
  if (cur.isNegative(balance.value)) return { status: 'paid' as const, label: 'Advance (credit)' }
  if (cur.isZero(balance.value)) return { status: 'paid' as const, label: 'Cleared' }
  return { status: 'arrears' as const, label: 'Owing' }
})
const shownBalance = computed(() => cur.format(cur.isNegative(balance.value) ? balance.value.replace('-', '') : balance.value))

// ---- record payment ------------------------------------------------------
const payOpen = ref(false)
const pay = useSubmit()
const pForm = reactive({ amount: '', source: 'manual', reference: '', note: '' })
function openPay() { Object.assign(pForm, { amount: '', source: 'manual', reference: '', note: '' }); pay.fields.value = {}; pay.message.value = ''; payOpen.value = true }
const payValid = computed(() => isValidMoneyInput(pForm.amount) && Number(pForm.amount) > 0)
async function savePayment() {
  if (!payValid.value) return
  const r = await pay.run(() => ledgers.recordPayment(props.unitId, {
    amount: pForm.amount, source: pForm.source as 'manual' | 'bank', ledger_type: type.value,
    ...(pForm.reference.trim() ? { reference: pForm.reference.trim() } : {}), ...(pForm.note.trim() ? { note: pForm.note.trim() } : {}),
  }))
  if (r) { payOpen.value = false; toast.success(`Payment recorded (${r.receipt}).`); page.value = 1; await load(); emit('changed') }
}

// ---- add charge ----------------------------------------------------------
const chargeOpen = ref(false)
const charge = useSubmit()
const cForm = reactive({ amount: '', description: '' })
function openCharge() { Object.assign(cForm, { amount: '', description: '' }); charge.fields.value = {}; charge.message.value = ''; chargeOpen.value = true }
const chargeValid = computed(() => isValidMoneyInput(cForm.amount) && Number(cForm.amount) > 0 && cForm.description.trim() !== '')
async function saveCharge() {
  if (!chargeValid.value) return
  const ok = await charge.run(async () => { await ledgers.addCharge(props.unitId, { amount: cForm.amount, description: cForm.description.trim(), ledger_type: type.value }); return true })
  if (ok) { chargeOpen.value = false; toast.success('Charge added.'); page.value = 1; await load(); emit('changed') }
}

// ---- reverse -------------------------------------------------------------
const target = ref<LedgerEntry | null>(null)
const reverseOpen = computed({ get: () => !!target.value, set: (v) => { if (!v) target.value = null } })
const reason = ref('')
const rev = useSubmit()
function openReverse(e: LedgerEntry) { target.value = e; reason.value = ''; rev.message.value = ''; rev.fields.value = {} }
async function reverse() {
  if (!target.value || !reason.value.trim()) return
  const id = await rev.run(() => ledgers.reverse(target.value!.id, reason.value.trim()))
  if (id) {
    target.value = null
    toast.success('Entry reversed. The original stays visible, struck through.')
    await load()
    emit('changed') // the rent overview and reports change too
  } else if (rev.status.value === 409) { toast.error(rev.message.value); target.value = null; await load() }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div v-if="types.length > 1" class="inline-flex rounded-sm border border-border-default bg-white p-1" role="group" aria-label="Ledger">
        <button v-for="t in types" :key="t.key" type="button" :aria-pressed="type === t.key"
          :class="['h-8 rounded-sm px-4 font-semibold transition-colors duration-(--duration-fast)', type === t.key ? 'bg-accent-primary-tint text-accent-text' : 'text-gray-500 hover:text-gray-900']" @click="type = t.key">{{ t.label }}</button>
      </div>
      <span v-else />
      <div class="flex gap-3">
        <UiButton variant="secondary" @click="openCharge"><Icon name="lucide:plus" class="size-4" />Add charge</UiButton>
        <UiButton @click="openPay"><Icon name="lucide:banknote" class="size-4" />Record payment</UiButton>
      </div>
    </div>

    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>

    <div v-if="data" class="card flex flex-wrap items-center justify-between gap-4 p-5">
      <div>
        <p class="text-[length:var(--text-caption)] text-gray-500">Balance</p>
        <p :class="['money text-[length:var(--text-money-lg)] font-bold', state.status === 'arrears' ? 'text-arrears-text' : 'text-paid-text']">Ksh {{ shownBalance }}</p>
      </div>
      <UiBadge :status="state.status" :label="state.label" />
    </div>

    <UiSkeleton v-if="loading && !data" block />
    <template v-else-if="data">
      <LedgerTable :entries="data.entries" @reverse="openReverse" />
      <UiPagination :meta="data.metadata" @change="(p) => (page = p)" />
    </template>

    <UiModal v-model="payOpen" title="Record a payment" width="sm">
      <form id="pay-form" class="flex flex-col gap-5" novalidate @submit.prevent="savePayment">
        <p class="text-gray-500">Cash or bank payment into the {{ types.find(t => t.key === type)?.label.toLowerCase() }} ledger. An overpayment stays as a credit.</p>
        <UiFormAlert v-if="pay.message.value">{{ pay.message.value }}</UiFormAlert>
        <UiField label="Amount" for="pm-amount" :error="pay.fields.value.amount"><UiInput id="pm-amount" v-model="pForm.amount" money :invalid="!!pay.fields.value.amount" /></UiField>
        <UiField label="Received by" for="pm-source"><UiSelect id="pm-source" v-model="pForm.source" :options="[{ value: 'manual', label: 'Cash at the office' }, { value: 'bank', label: 'Bank deposit' }]" /></UiField>
        <UiField label="Reference" for="pm-ref" optional help="Receipt or bank slip number. The same reference cannot be recorded twice." :error="pay.fields.value.reference"><UiInput id="pm-ref" v-model="pForm.reference" /></UiField>
        <UiField label="Note" for="pm-note" optional><UiInput id="pm-note" v-model="pForm.note" /></UiField>
      </form>
      <template #footer><UiButton variant="secondary" @click="payOpen = false">Cancel</UiButton><UiButton type="submit" form="pay-form" :loading="pay.loading.value" :disabled="!payValid" @click="savePayment">Record payment</UiButton></template>
    </UiModal>

    <UiModal v-model="chargeOpen" title="Add a charge" width="sm">
      <form id="charge-form" class="flex flex-col gap-5" novalidate @submit.prevent="saveCharge">
        <p class="text-gray-500">A penalty, or the corrected entry after a reversal.</p>
        <UiFormAlert v-if="charge.message.value">{{ charge.message.value }}</UiFormAlert>
        <UiField label="Amount" for="ch-amount" :error="charge.fields.value.amount"><UiInput id="ch-amount" v-model="cForm.amount" money :invalid="!!charge.fields.value.amount" /></UiField>
        <UiField label="Description" for="ch-desc" :error="charge.fields.value.description"><UiInput id="ch-desc" v-model="cForm.description" :invalid="!!charge.fields.value.description" /></UiField>
      </form>
      <template #footer><UiButton variant="secondary" @click="chargeOpen = false">Cancel</UiButton><UiButton type="submit" form="charge-form" :loading="charge.loading.value" :disabled="!chargeValid" @click="saveCharge">Add charge</UiButton></template>
    </UiModal>

    <UiModal v-model="reverseOpen" title="Reverse this entry?" width="sm">
      <p v-if="target" class="mb-5 text-gray-700">{{ target.description }} &middot; Ksh {{ cur.format(target.amount) }} ({{ target.direction === 'DEBIT' ? 'debit' : 'credit' }}). The entry stays on the ledger, marked reversed, and a mirrored entry with your reason is added.</p>
      <UiFormAlert v-if="rev.message.value" class="mb-5">{{ rev.message.value }}</UiFormAlert>
      <UiField label="Reason" for="rv-reason" :error="rev.fields.value.reason"><UiInput id="rv-reason" v-model="reason" placeholder="e.g. Cheque bounced" :invalid="!!rev.fields.value.reason" /></UiField>
      <template #footer><UiButton variant="secondary" @click="target = null">Cancel</UiButton><UiButton variant="destructive" :loading="rev.loading.value" :disabled="!reason.trim()" @click="reverse">Reverse entry</UiButton></template>
    </UiModal>
  </div>
</template>
