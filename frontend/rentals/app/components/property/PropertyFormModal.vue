<script setup lang="ts">
import type { Property } from '~/types/api'
import { isValidMoneyInput } from '~/utils/money'

const open = defineModel<boolean>({ default: false })
const emit = defineEmits<{ created: [Property] }>()

const store = useProperties()
const toast = useToast()
const { loading, fields, message, run } = useSubmit()

const form = reactive({
  landlord_id: '', name: '', location: '', slug: '', garbage_enabled: false, garbage_fee: '0', water_rate_per_unit: '', management_fee_percent: '5', payhero_channel_id: '',
})
const slugTouched = ref(false)
const addLandlord = ref(false)

watch(() => form.name, (n) => { if (!slugTouched.value) form.slug = slugify(n) })
watch(open, async (v) => {
  if (!v) return
  Object.assign(form, { landlord_id: '', name: '', location: '', slug: '', garbage_enabled: false, garbage_fee: '0', water_rate_per_unit: '', management_fee_percent: '5', payhero_channel_id: '' })
  slugTouched.value = false
  fields.value = {}
  message.value = ''
  if (!store.landlords.length) await store.fetchLandlords().catch(toast.fail)
})

const localErrors = computed(() => {
  const e: Record<string, string> = {}
  if (form.water_rate_per_unit && !isValidMoneyInput(form.water_rate_per_unit)) e.water_rate_per_unit = 'Use a number with up to 2 decimals'
  if (form.garbage_enabled && (!isValidMoneyInput(form.garbage_fee) || Number(form.garbage_fee) <= 0)) e.garbage_fee = 'Enter a fee above 0'
  const pct = Number(form.management_fee_percent)
  if (form.management_fee_percent === '' || Number.isNaN(pct) || pct < 0 || pct > 100) e.management_fee_percent = 'Between 0 and 100'
  return e
})
const err = (k: string) => fields.value[k] ?? localErrors.value[k]
const valid = computed(() => form.landlord_id && form.name && form.location && form.slug && form.water_rate_per_unit && !Object.keys(localErrors.value).length)

async function save() {
  if (!valid.value) return
  const created = await run(() => store.create({
    landlord_id: form.landlord_id, name: form.name.trim(), location: form.location.trim(), slug: form.slug,
    garbage_enabled: form.garbage_enabled, garbage_fee: form.garbage_enabled ? form.garbage_fee : '0',
    water_rate_per_unit: form.water_rate_per_unit, management_fee_percent: Number(form.management_fee_percent),
    ...(form.payhero_channel_id.trim() ? { payhero_channel_id: form.payhero_channel_id.trim() } : {}),
  }))
  if (created) {
    toast.success(`${created.name} created`)
    emit('created', created)
    open.value = false
  }
}
</script>

<template>
  <UiModal v-model="open" title="New property" width="lg">
    <form id="property-form" class="grid gap-5 sm:grid-cols-2" novalidate @submit.prevent="save">
      <UiFormAlert v-if="message" class="sm:col-span-2">{{ message }}</UiFormAlert>

      <UiField label="Landlord" for="p-landlord" :error="err('landlord_id')" class="sm:col-span-2">
        <UiSelect id="p-landlord" v-model="form.landlord_id" placeholder="Choose a landlord" :invalid="!!err('landlord_id')" :options="store.landlords.map(l => ({ value: l.id, label: l.name }))" />
        <button type="button" class="mt-2 font-semibold text-accent-text" @click="addLandlord = true">+ Add a new landlord</button>
      </UiField>

      <UiField label="Property name" for="p-name" :error="err('name')"><UiInput id="p-name" v-model="form.name" :invalid="!!err('name')" /></UiField>
      <UiField label="Location" for="p-loc" :error="err('location')"><UiInput id="p-loc" v-model="form.location" :invalid="!!err('location')" /></UiField>
      <UiField label="Pay-page link name" for="p-slug" help="Tenants pay at /pay/<this>/<unit>" :error="err('slug')" class="sm:col-span-2">
        <UiInput id="p-slug" v-model="form.slug" :invalid="!!err('slug')" @update:model-value="slugTouched = true" />
      </UiField>

      <UiField label="Water rate per unit" for="p-water" :error="err('water_rate_per_unit')"><UiInput id="p-water" v-model="form.water_rate_per_unit" money :invalid="!!err('water_rate_per_unit')" /></UiField>
      <UiField label="Management fee (%)" for="p-fee" :error="err('management_fee_percent')"><UiInput id="p-fee" v-model="form.management_fee_percent" inputmode="decimal" :invalid="!!err('management_fee_percent')" /></UiField>

      <div class="flex items-center gap-3 sm:col-span-2">
        <input id="p-garbage" v-model="form.garbage_enabled" type="checkbox" class="size-5 accent-accent-primary">
        <label for="p-garbage">Charge garbage collection</label>
      </div>
      <UiField v-if="form.garbage_enabled" label="Garbage fee per unit" for="p-gfee" :error="err('garbage_fee')" class="sm:col-span-2"><UiInput id="p-gfee" v-model="form.garbage_fee" money :invalid="!!err('garbage_fee')" /></UiField>

      <UiField label="PayHero channel ID" for="p-channel" optional help="Where this property's M-Pesa payments arrive" :error="err('payhero_channel_id')" class="sm:col-span-2">
        <UiInput id="p-channel" v-model="form.payhero_channel_id" :invalid="!!err('payhero_channel_id')" />
      </UiField>
    </form>
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton type="submit" form="property-form" :loading="loading" :disabled="!valid" @click="save">Create property</UiButton>
    </template>
  </UiModal>
  <LandlordFormModal v-model="addLandlord" @saved="(l) => (form.landlord_id = l.id)" />
</template>
