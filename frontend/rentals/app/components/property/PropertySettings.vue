<script setup lang="ts">
import type { Property, PrintTheme } from '~/types/api'
import { isValidMoneyInput } from '~/utils/money'

const props = defineProps<{ property: Property }>()
const emit = defineEmits<{ reload: [] }>()

const store = useProperties()
const toast = useToast()

// ---- details -------------------------------------------------------------
const details = useSubmit()
const form = reactive({ landlord_id: '', name: '', location: '', slug: '', water_rate_per_unit: '', management_fee_percent: '' })
function fill() {
  const p = props.property
  Object.assign(form, { landlord_id: p.landlord_id, name: p.name, location: p.location, slug: p.slug, water_rate_per_unit: p.water_rate_per_unit, management_fee_percent: String(p.management_fee_percent) })
}
watch(() => props.property, fill, { immediate: true })
onMounted(() => { if (!store.landlords.length) store.fetchLandlords().catch(toast.fail) })

const changes = computed(() => {
  const p = props.property
  const c: Record<string, string | number> = {}
  if (form.landlord_id !== p.landlord_id) c.landlord_id = form.landlord_id
  if (form.name.trim() !== p.name) c.name = form.name.trim()
  if (form.location.trim() !== p.location) c.location = form.location.trim()
  if (form.slug !== p.slug) c.slug = form.slug
  if (form.water_rate_per_unit !== p.water_rate_per_unit) c.water_rate_per_unit = form.water_rate_per_unit
  if (Number(form.management_fee_percent) !== p.management_fee_percent) c.management_fee_percent = Number(form.management_fee_percent)
  return c
})
const rateError = computed(() => (form.water_rate_per_unit && !isValidMoneyInput(form.water_rate_per_unit) ? 'Use a number with up to 2 decimals' : ''))

// A stale edit is a 409: tell the manager and reload the fresh record.
async function guard<T>(s: ReturnType<typeof useSubmit>, fn: () => Promise<T>) {
  const r = await s.run(fn)
  if (r === undefined && s.status.value === 409) { toast.error('Someone else changed this property. Reloaded the latest version.'); emit('reload') }
  return r
}

async function saveDetails() {
  if (!Object.keys(changes.value).length || rateError.value) return
  const r = await guard(details, () => store.update(props.property.id, changes.value))
  if (r) { toast.success('Property saved'); emit('reload') }
}

// ---- garbage -------------------------------------------------------------
const garbage = useSubmit()
const gEnabled = ref(props.property.garbage_enabled)
const gFee = ref(props.property.garbage_fee)
watch(() => props.property, (p) => { gEnabled.value = p.garbage_enabled; gFee.value = p.garbage_fee })
const gError = computed(() => (gEnabled.value && (!isValidMoneyInput(gFee.value) || Number(gFee.value) <= 0) ? 'Enter a fee above 0' : ''))
const gDirty = computed(() => gEnabled.value !== props.property.garbage_enabled || (gEnabled.value && gFee.value !== props.property.garbage_fee))

async function saveGarbage() {
  if (gError.value) return
  const r = await guard(garbage, () => store.setGarbage(props.property.id, gEnabled.value, gEnabled.value ? gFee.value : undefined))
  if (r) { toast.success('Garbage settings saved'); emit('reload') }
}

// ---- electricity deposit ---------------------------------------------------
const electricity = useSubmit()
const eEnabled = ref(props.property.electricity_enabled)
const eAmount = ref(props.property.electricity_deposit_amount)
watch(() => props.property, (p) => { eEnabled.value = p.electricity_enabled; eAmount.value = p.electricity_deposit_amount })
const eError = computed(() => (eEnabled.value && (!isValidMoneyInput(eAmount.value) || Number(eAmount.value) <= 0) ? 'Enter a deposit amount above 0' : ''))
const eDirty = computed(() => eEnabled.value !== props.property.electricity_enabled || (eEnabled.value && eAmount.value !== props.property.electricity_deposit_amount))

async function saveElectricity() {
  if (eError.value) return
  const r = await guard(electricity, () => store.setElectricity(props.property.id, eEnabled.value, eEnabled.value ? eAmount.value : undefined))
  if (r) {
    const chargedNote = r.leases_charged > 0 ? ` ${r.leases_charged} existing lease(s) were charged the deposit.` : ''
    toast.success('Electricity settings saved.' + chargedNote)
    emit('reload')
  }
}

// ---- print theme ---------------------------------------------------------
const theme = useSubmit()
const t = reactive({ header_text: '', address: '', phone: '', accent_color: '#1F4E79', reconnection_note: '' })
function fillTheme() {
  const pt = props.property.print_theme
  Object.assign(t, { header_text: pt?.header_text ?? '', address: (pt?.address_lines ?? []).join('\n'), phone: pt?.phone ?? '', accent_color: pt?.accent_color ?? '#1F4E79', reconnection_note: pt?.reconnection_note ?? '' })
}
watch(() => props.property, fillTheme, { immediate: true })
const addressLines = computed(() => t.address.split('\n').map(l => l.trim()).filter(Boolean))
const themeError = computed(() => (addressLines.value.length > 4 ? 'At most 4 address lines' : ''))

async function saveTheme() {
  if (themeError.value) return
  const body: PrintTheme = { header_text: t.header_text.trim(), address_lines: addressLines.value, phone: t.phone.trim(), accent_color: t.accent_color, reconnection_note: t.reconnection_note.trim() }
  const r = await guard(theme, () => store.setPrintTheme(props.property.id, body))
  if (r) { toast.success('Print theme saved'); emit('reload') }
}
async function clearTheme() {
  const r = await guard(theme, () => store.setPrintTheme(props.property.id, null))
  if (r) { toast.success('Print theme cleared'); emit('reload') }
}

// ---- archive -------------------------------------------------------------
const confirm = ref(false)
const archiving = useSubmit()
async function archive() {
  const ok = await archiving.run(async () => { await store.archive(props.property.id); return true })
  // The store clears the loaded property, which unmounts this panel: leave from here.
  if (ok) { toast.success('Property archived. Its history is kept.'); await navigateTo('/properties') }
  else confirm.value = false
}
</script>

<template>
  <div class="grid max-w-4xl gap-8">
    <UiCard>
      <h2 class="mb-6">Property details</h2>
      <form class="grid gap-5 sm:grid-cols-2" novalidate @submit.prevent="saveDetails">
        <UiFormAlert v-if="details.message.value" class="sm:col-span-2">{{ details.message.value }}</UiFormAlert>
        <UiField label="Landlord" for="s-landlord" :error="details.fields.value.landlord_id" class="sm:col-span-2">
          <UiSelect id="s-landlord" v-model="form.landlord_id" :options="store.landlords.map(l => ({ value: l.id, label: l.name }))" />
        </UiField>
        <UiField label="Name" for="s-name" :error="details.fields.value.name"><UiInput id="s-name" v-model="form.name" :invalid="!!details.fields.value.name" /></UiField>
        <UiField label="Location" for="s-loc" :error="details.fields.value.location"><UiInput id="s-loc" v-model="form.location" :invalid="!!details.fields.value.location" /></UiField>
        <UiField label="Pay-page link name" for="s-slug" help="Changing this changes the tenants' pay link." :error="details.fields.value.slug" class="sm:col-span-2"><UiInput id="s-slug" v-model="form.slug" :invalid="!!details.fields.value.slug" /></UiField>
        <UiField label="Water rate per unit" for="s-rate" :error="rateError || details.fields.value.water_rate_per_unit"><UiInput id="s-rate" v-model="form.water_rate_per_unit" money :invalid="!!rateError" /></UiField>
        <UiField label="Management fee (%)" for="s-fee" :error="details.fields.value.management_fee_percent"><UiInput id="s-fee" v-model="form.management_fee_percent" inputmode="decimal" :invalid="!!details.fields.value.management_fee_percent" /></UiField>
        <div class="sm:col-span-2"><UiButton type="submit" :loading="details.loading.value" :disabled="!Object.keys(changes).length || !!rateError">Save changes</UiButton></div>
      </form>
    </UiCard>

    <PropertyGetPaidCard :property="property" @reload="emit('reload')" />

    <UiCard>
      <h2 class="mb-1">Garbage collection</h2>
      <p class="mb-6 text-gray-500">When on, each occupied unit is billed the fee with the monthly garbage run.</p>
      <form class="flex max-w-md flex-col gap-5" novalidate @submit.prevent="saveGarbage">
        <UiFormAlert v-if="garbage.message.value">{{ garbage.message.value }}</UiFormAlert>
        <div class="flex items-center gap-3"><input id="g-on" v-model="gEnabled" type="checkbox" class="size-5 accent-accent-primary"><label for="g-on">Charge garbage collection</label></div>
        <UiField v-if="gEnabled" label="Fee per unit" for="g-fee" :error="gError || garbage.fields.value.fee"><UiInput id="g-fee" v-model="gFee" money :invalid="!!gError" /></UiField>
        <div><UiButton type="submit" :loading="garbage.loading.value" :disabled="!gDirty || !!gError">Save</UiButton></div>
      </form>
    </UiCard>

    <UiCard>
      <h2 class="mb-1">Electricity deposit</h2>
      <p class="mb-6 text-gray-500">When on, new leases are charged a one-off refundable deposit (not a monthly bill). Turning it on also charges any existing active lease that doesn't have one yet.</p>
      <form class="flex max-w-md flex-col gap-5" novalidate @submit.prevent="saveElectricity">
        <UiFormAlert v-if="electricity.message.value">{{ electricity.message.value }}</UiFormAlert>
        <div class="flex items-center gap-3"><input id="e-on" v-model="eEnabled" type="checkbox" class="size-5 accent-accent-primary"><label for="e-on">Charge electricity deposit</label></div>
        <UiField v-if="eEnabled" label="Default deposit amount" for="e-amount" :error="eError || electricity.fields.value.deposit_amount"><UiInput id="e-amount" v-model="eAmount" money :invalid="!!eError" /></UiField>
        <div><UiButton type="submit" :loading="electricity.loading.value" :disabled="!eDirty || !!eError">Save</UiButton></div>
      </form>
    </UiCard>

    <UiCard>
      <h2 class="mb-1">Print theme</h2>
      <p class="mb-6 text-gray-500">Tints the header of printed bills and the schedule. Documents stay black on white.</p>
      <div class="grid gap-8 lg:grid-cols-2">
        <form class="flex flex-col gap-5" novalidate @submit.prevent="saveTheme">
          <UiFormAlert v-if="theme.message.value">{{ theme.message.value }}</UiFormAlert>
          <UiField label="Header text" for="t-head" :error="theme.fields.value.header_text"><UiInput id="t-head" v-model="t.header_text" /></UiField>
          <UiField label="Address lines" for="t-addr" help="One per line, up to 4" :error="themeError || theme.fields.value.address_lines">
            <textarea id="t-addr" v-model="t.address" rows="3" class="w-full rounded-sm border border-border-default bg-white p-4 outline-none focus:border-accent-primary focus:ring-2 focus:ring-accent-primary-tint" />
          </UiField>
          <UiField label="Phone" for="t-phone" :error="theme.fields.value.phone"><UiInput id="t-phone" v-model="t.phone" type="tel" /></UiField>
          <UiField label="Accent colour" for="t-accent" :error="theme.fields.value.accent_color">
            <input id="t-accent" v-model="t.accent_color" type="color" class="h-11 w-24 cursor-pointer rounded-sm border border-border-default bg-white p-1">
          </UiField>
          <UiField label="Reconnection note" for="t-note" optional :error="theme.fields.value.reconnection_note"><UiInput id="t-note" v-model="t.reconnection_note" /></UiField>
          <div class="flex gap-3">
            <UiButton type="submit" :loading="theme.loading.value" :disabled="!!themeError">Save theme</UiButton>
            <UiButton v-if="property.print_theme" variant="secondary" @click="clearTheme">Clear theme</UiButton>
          </div>
        </form>

        <div aria-label="Print preview">
          <p class="th-text mb-2">Preview</p>
          <div class="rounded-sm border border-border-default bg-white p-6 text-black">
            <div class="border-b-2 pb-3" :style="{ borderColor: t.accent_color }">
              <p class="text-[length:var(--text-h2)] font-bold" :style="{ color: t.accent_color }">{{ t.header_text || property.name }}</p>
              <p v-for="l in addressLines" :key="l" class="text-[length:var(--text-caption)]">{{ l }}</p>
              <p v-if="t.phone" class="text-[length:var(--text-caption)]">Tel: {{ t.phone }}</p>
            </div>
            <p class="mt-4 font-semibold">WATER BILL &mdash; G1</p>
            <div class="mt-2 h-2 w-2/3 rounded-full bg-gray-100" /><div class="mt-2 h-2 w-1/2 rounded-full bg-gray-100" />
            <p v-if="t.reconnection_note" class="mt-4 text-[length:var(--text-caption)]">{{ t.reconnection_note }}</p>
          </div>
        </div>
      </div>
    </UiCard>

    <UiCard>
      <h2 class="mb-1">Archive property</h2>
      <p class="mb-4 text-gray-500">Hides the property from lists. Its history is kept. Not possible while any unit has an active lease.</p>
      <UiButton variant="destructive" @click="confirm = true">Archive property</UiButton>
    </UiCard>

    <UiConfirmDialog v-model="confirm" title="Archive this property?" :text="`${property.name} will be hidden. History is kept.`" confirm-label="Archive" destructive :loading="archiving.loading.value" @confirm="archive">
      <UiFormAlert v-if="archiving.message.value" class="mt-4">{{ archiving.message.value }}</UiFormAlert>
    </UiConfirmDialog>
  </div>
</template>
