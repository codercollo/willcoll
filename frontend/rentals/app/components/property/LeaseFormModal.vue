<script setup lang="ts">
import type { Lease } from '~/types/api'
import { isValidMoneyInput } from '~/utils/money'

// Create a lease (with deposits and payers) or edit one (name, phone, rent).
// Deposits and start date are on the ledger, so they cannot be edited.
const open = defineModel<boolean>({ default: false })
const props = defineProps<{ unitId: string; lease?: Lease | null; electricityEnabled?: boolean; electricityDepositDefault?: string }>()
const emit = defineEmits<{ saved: [Lease] }>()

const units = useUnits()
const toast = useToast()
const { loading, fields, message, run, status } = useSubmit()
const editing = computed(() => !!props.lease)

const form = reactive({ tenant_name: '', primary_phone: '', rent_amount: '', rent_deposit_amount: '', water_deposit_amount: '', electricity_deposit_amount: '', start_date: '' })
const payers = ref<{ name: string; phone: string }[]>([])

watch(open, (v) => {
  if (!v) return
  const l = props.lease
  Object.assign(form, {
    tenant_name: l?.tenant_name ?? '', primary_phone: l?.primary_phone ?? '', rent_amount: l?.rent_amount ?? '',
    rent_deposit_amount: l?.rent_deposit_amount ?? '', water_deposit_amount: l?.water_deposit_amount ?? '0',
    electricity_deposit_amount: l?.electricity_deposit_amount ?? props.electricityDepositDefault ?? '0',
    start_date: l?.start_date?.slice(0, 10) ?? new Date().toISOString().slice(0, 10),
  })
  payers.value = []
  fields.value = {}
  message.value = ''
})

function normalisePhone(p: string) {
  const d = p.replace(/[\s-]/g, '')
  if (/^0[17]\d{8}$/.test(d)) return '+254' + d.slice(1)
  if (/^254\d{9}$/.test(d)) return '+' + d
  return d
}

const local = computed(() => {
  const e: Record<string, string> = {}
  for (const k of ['rent_amount', 'rent_deposit_amount', 'water_deposit_amount', 'electricity_deposit_amount'] as const) {
    if (editing.value && k !== 'rent_amount') continue
    if (form[k] !== '' && !isValidMoneyInput(form[k])) e[k] = 'Use a number with up to 2 decimals'
  }
  if (payers.value.length > 5) e.payers = 'At most 5 co-payers'
  return e
})
const err = (k: string) => fields.value[k] ?? local.value[k]
const valid = computed(() => form.tenant_name.trim() && form.primary_phone.trim() && form.rent_amount !== '' && !Object.keys(local.value).length
  && (editing.value || (form.rent_deposit_amount !== '' && form.water_deposit_amount !== '' && form.start_date)))

async function save() {
  if (!valid.value) return
  const lease = await run(() => {
    if (props.lease) return units.updateLease(props.lease.id, { tenant_name: form.tenant_name.trim(), primary_phone: normalisePhone(form.primary_phone), rent_amount: form.rent_amount })
    return units.createLease(props.unitId, {
      tenant_name: form.tenant_name.trim(), primary_phone: normalisePhone(form.primary_phone), rent_amount: form.rent_amount,
      rent_deposit_amount: form.rent_deposit_amount, water_deposit_amount: form.water_deposit_amount,
      ...(props.electricityEnabled ? { electricity_deposit_amount: form.electricity_deposit_amount } : {}),
      start_date: form.start_date,
      payers: payers.value.filter(p => p.name.trim()).map(p => ({ name: p.name.trim(), ...(p.phone.trim() ? { phone: normalisePhone(p.phone) } : {}) })),
    })
  })
  if (lease) { toast.success(editing.value ? 'Lease updated' : 'Lease started'); emit('saved', lease); open.value = false }
  else if (status.value === 409) toast.error(message.value)
}
</script>

<template>
  <UiModal v-model="open" :title="editing ? 'Edit lease' : 'New lease'" width="lg">
    <form id="lease-form" class="grid gap-5 sm:grid-cols-2" novalidate @submit.prevent="save">
      <UiFormAlert v-if="message" class="sm:col-span-2">{{ message }}</UiFormAlert>
      <UiField label="Tenant name" for="ls-name" :error="err('tenant_name')"><UiInput id="ls-name" v-model="form.tenant_name" :invalid="!!err('tenant_name')" /></UiField>
      <UiField label="Phone" for="ls-phone" help="The number that pays by M-Pesa" :error="err('primary_phone')"><UiInput id="ls-phone" v-model="form.primary_phone" type="tel" :invalid="!!err('primary_phone')" /></UiField>
      <UiField label="Monthly rent" for="ls-rent" :error="err('rent_amount')"><UiInput id="ls-rent" v-model="form.rent_amount" money :invalid="!!err('rent_amount')" /></UiField>

      <template v-if="!editing">
        <UiField label="Start date" for="ls-start" :error="err('start_date')"><UiInput id="ls-start" v-model="form.start_date" type="date" :invalid="!!err('start_date')" /></UiField>
        <UiField label="Rent deposit" for="ls-rd" :error="err('rent_deposit_amount')"><UiInput id="ls-rd" v-model="form.rent_deposit_amount" money :invalid="!!err('rent_deposit_amount')" /></UiField>
        <UiField label="Water deposit" for="ls-wd" :error="err('water_deposit_amount')"><UiInput id="ls-wd" v-model="form.water_deposit_amount" money :invalid="!!err('water_deposit_amount')" /></UiField>
        <UiField v-if="electricityEnabled" label="Electricity deposit" for="ls-ed" :error="err('electricity_deposit_amount')"><UiInput id="ls-ed" v-model="form.electricity_deposit_amount" money :invalid="!!err('electricity_deposit_amount')" /></UiField>

        <fieldset class="flex flex-col gap-3 sm:col-span-2">
          <legend class="mb-1 font-semibold">Co-payers <span class="font-normal text-gray-500">(optional, up to 5)</span></legend>
          <p class="text-gray-500">People who may also pay this rent. Their phones identify M-Pesa payments and can request the pay-page code.</p>
          <div v-for="(p, i) in payers" :key="i" class="grid grid-cols-[1fr_1fr_auto] items-start gap-3">
            <UiInput v-model="p.name" placeholder="Name" :aria-label="`Co-payer ${i + 1} name`" />
            <UiInput v-model="p.phone" type="tel" placeholder="Phone" :aria-label="`Co-payer ${i + 1} phone`" />
            <UiButton variant="icon" :label="`Remove co-payer ${i + 1}`" @click="payers.splice(i, 1)"><Icon name="lucide:trash-2" class="size-5" /></UiButton>
          </div>
          <p v-if="err('payers')" class="text-arrears-text" role="alert">{{ err('payers') }}</p>
          <div><UiButton variant="secondary" :disabled="payers.length >= 5" @click="payers.push({ name: '', phone: '' })"><Icon name="lucide:plus" class="size-4" />Add co-payer</UiButton></div>
        </fieldset>
        <p class="text-gray-500 sm:col-span-2">Starting the lease marks the unit occupied and posts the deposit charges to its ledger.</p>
      </template>
    </form>
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton type="submit" form="lease-form" :loading="loading" :disabled="!valid" @click="save">{{ editing ? 'Save changes' : 'Start lease' }}</UiButton>
    </template>
  </UiModal>
</template>
