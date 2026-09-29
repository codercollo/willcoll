<script setup lang="ts">
import type { AdminPlan, PlanInput } from '~/types/admin'
import { isWholeShillings } from '~/utils/pay'

// Create or edit a plan. Money is WHOLE shillings; a new price applies to
// invoices raised afterwards only, and plans are never deleted (archived).
const open = defineModel<boolean>({ default: false })
const props = defineProps<{ plan?: AdminPlan | null }>()
const emit = defineEmits<{ saved: [AdminPlan] }>()

const admin = useAdmin()
const toast = useToast()
const { loading, fields, message, run } = useSubmit()
const form = reactive({
  name: '', price: '', interval: 'monthly', cap: '',
  pricingType: 'flat' as 'flat' | 'per_unit', perUnitPrice: '', minPrice: '',
  isTest: false, sortOrder: '0',
})
const editing = computed(() => !!props.plan)
// The test-plan checkbox only makes sense where the backend would honor it:
// this is a UI nicety only — the backend refuses is_test outside development
// regardless of what this form sends.
const isDev = import.meta.dev

watch(open, (v) => {
  if (!v) return
  const p = props.plan
  Object.assign(form, {
    name: p?.name ?? '', price: p ? String(Number(p.price)) : '', interval: p?.billing_interval ?? 'monthly', cap: p?.unit_cap ? String(p.unit_cap) : '',
    pricingType: p?.pricing_type ?? 'flat', perUnitPrice: p?.per_unit_price ? String(Number(p.per_unit_price)) : '', minPrice: p?.min_price ? String(Number(p.min_price)) : '',
    isTest: p?.is_test ?? false, sortOrder: String(p?.sort_order ?? 0),
  })
  fields.value = {}
  message.value = ''
})

const perUnit = computed(() => form.pricingType === 'per_unit')

const local = computed(() => {
  const e: Record<string, string> = {}
  if (perUnit.value) {
    if (form.perUnitPrice !== '' && !isWholeShillings(form.perUnitPrice)) e.per_unit_price = 'Whole shillings only, above 0'
    if (form.minPrice !== '' && !isWholeShillings(form.minPrice)) e.min_price = 'Whole shillings only, above 0'
  } else if (form.price !== '' && !isWholeShillings(form.price)) {
    e.price = 'Whole shillings only, above 0'
  }
  if (form.cap !== '' && !/^[1-9]\d{0,6}$/.test(form.cap)) e.unit_cap = 'A whole number above 0, or leave empty for no limit'
  return e
})
const err = (k: string) => fields.value[k] ?? local.value[k]
const valid = computed(() => {
  if (Object.keys(local.value).length || !form.name.trim()) return false
  return perUnit.value ? form.perUnitPrice !== '' && form.minPrice !== '' : form.price !== ''
})

async function save() {
  if (!valid.value) return
  const cap = form.cap === '' ? null : Number(form.cap)
  const saved = await run(() => {
    if (props.plan) {
      const patch: PlanInput = {}
      if (form.name.trim() !== props.plan.name) patch.name = form.name.trim()
      if (form.interval !== props.plan.billing_interval) patch.billing_interval = form.interval
      if (cap !== (props.plan.unit_cap ?? null)) patch.unit_cap = cap
      if (form.pricingType !== props.plan.pricing_type) patch.pricing_type = form.pricingType
      if (!perUnit.value && Number(form.price) !== Number(props.plan.price)) patch.price = form.price
      if (perUnit.value && form.perUnitPrice !== String(Number(props.plan.per_unit_price ?? 0))) patch.per_unit_price = form.perUnitPrice
      if (perUnit.value && form.minPrice !== String(Number(props.plan.min_price ?? 0))) patch.min_price = form.minPrice
      if (isDev && form.isTest !== props.plan.is_test) patch.is_test = form.isTest
      if (Number(form.sortOrder) !== props.plan.sort_order) patch.sort_order = Number(form.sortOrder)
      return admin.updatePlan(props.plan.id, patch)
    }
    const input: PlanInput = {
      name: form.name.trim(), billing_interval: form.interval, pricing_type: form.pricingType,
      sort_order: Number(form.sortOrder) || 0, ...(cap ? { unit_cap: cap } : {}),
    }
    if (perUnit.value) { input.per_unit_price = form.perUnitPrice; input.min_price = form.minPrice }
    else input.price = form.price
    if (isDev && form.isTest) input.is_test = true
    return admin.createPlan(input)
  })
  if (saved) { toast.success(editing.value ? 'Plan updated' : 'Plan created'); emit('saved', saved); open.value = false }
}
</script>

<template>
  <UiModal v-model="open" :title="editing ? 'Edit plan' : 'New plan'" width="sm">
    <form id="plan-form" class="flex flex-col gap-5" novalidate @submit.prevent="save">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiFormAlert v-if="editing" tone="info">A new price applies to invoices raised from now on. Existing subscribers keep their current period.</UiFormAlert>
      <UiField label="Name" for="pl-name" :error="err('name')"><UiInput id="pl-name" v-model="form.name" :invalid="!!err('name')" /></UiField>

      <UiField label="Pricing type" for="pl-pricing-type">
        <UiSelect id="pl-pricing-type" v-model="form.pricingType" :options="[{ value: 'flat', label: 'Flat' }, { value: 'per_unit', label: 'Per unit' }]" />
      </UiField>

      <UiField v-if="!perUnit" label="Price" for="pl-price" :error="err('price')">
        <UiInput id="pl-price" v-model="form.price" money inputmode="numeric" :invalid="!!err('price')" />
      </UiField>
      <template v-else>
        <UiField label="Price per unit" for="pl-per-unit-price" :error="err('per_unit_price')">
          <UiInput id="pl-per-unit-price" v-model="form.perUnitPrice" money inputmode="numeric" :invalid="!!err('per_unit_price')" />
        </UiField>
        <UiField label="Minimum per month" for="pl-min-price" :error="err('min_price')">
          <UiInput id="pl-min-price" v-model="form.minPrice" money inputmode="numeric" :invalid="!!err('min_price')" />
        </UiField>
      </template>

      <UiField label="Billed" for="pl-int"><UiSelect id="pl-int" v-model="form.interval" :options="[{ value: 'weekly', label: 'Weekly' }, { value: 'monthly', label: 'Monthly' }, { value: 'quarterly', label: 'Quarterly' }, { value: 'yearly', label: 'Yearly' }]" /></UiField>
      <UiField label="Unit limit" for="pl-cap" optional help="Empty means no limit." :error="err('unit_cap')"><UiInput id="pl-cap" v-model="form.cap" inputmode="numeric" :invalid="!!err('unit_cap')" /></UiField>
      <UiField label="Display order" for="pl-sort" optional help="Lower numbers show first to managers."><UiInput id="pl-sort" v-model="form.sortOrder" inputmode="numeric" /></UiField>

      <label v-if="isDev" class="flex items-center gap-2 text-sm text-gray-700">
        <input v-model="form.isTest" type="checkbox" class="size-4 rounded border-border-default">
        Test plan
      </label>
      <p v-if="isDev" class="-mt-3 text-[length:var(--text-caption)] text-gray-500">Only visible to managers in development.</p>
    </form>
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton type="submit" form="plan-form" :loading="loading" :disabled="!valid" @click="save">{{ editing ? 'Save changes' : 'Create plan' }}</UiButton>
    </template>
  </UiModal>
</template>
