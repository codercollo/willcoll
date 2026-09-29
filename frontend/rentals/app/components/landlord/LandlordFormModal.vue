<script setup lang="ts">
import type { Landlord } from '~/types/api'

const open = defineModel<boolean>({ default: false })
const props = defineProps<{ landlord?: Landlord | null }>()
const emit = defineEmits<{ saved: [Landlord] }>()

const store = useProperties()
const toast = useToast()
const { loading, fields, message, run } = useSubmit()

const form = reactive({ name: '', phone: '', email: '', bank_name: '', bank_account_name: '', bank_account_number: '' })

watch(open, (v) => {
  if (!v) return
  const l = props.landlord
  Object.assign(form, { name: l?.name ?? '', phone: l?.phone ?? '', email: l?.email ?? '', bank_name: l?.bank_name ?? '', bank_account_name: l?.bank_account_name ?? '', bank_account_number: l?.bank_account_number ?? '' })
  fields.value = {}
  message.value = ''
})

async function save() {
  // Optional fields are omitted when empty rather than sent as "".
  const body: Record<string, string> = { name: form.name.trim(), phone: form.phone.trim() }
  for (const k of ['email', 'bank_name', 'bank_account_name', 'bank_account_number'] as const) if (form[k].trim()) body[k] = form[k].trim()
  const saved = await run(() => (props.landlord ? store.updateLandlord(props.landlord.id, body) : store.createLandlord(body as never)))
  if (saved) {
    toast.success(props.landlord ? 'Landlord updated' : 'Landlord added')
    emit('saved', saved)
    open.value = false
  }
}
</script>

<template>
  <UiModal v-model="open" :title="landlord ? 'Edit landlord' : 'Add landlord'">
    <form id="landlord-form" class="flex flex-col gap-5" novalidate @submit.prevent="save">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Name" for="l-name" :error="fields.name"><UiInput id="l-name" v-model="form.name" :invalid="!!fields.name" /></UiField>
      <UiField label="Phone" for="l-phone" help="+254 format" :error="fields.phone"><UiInput id="l-phone" v-model="form.phone" type="tel" :invalid="!!fields.phone" /></UiField>
      <UiField label="Email" for="l-email" optional :error="fields.email"><UiInput id="l-email" v-model="form.email" type="email" :invalid="!!fields.email" /></UiField>
      <p class="-mb-2 text-gray-500">Bank details print in the &ldquo;pay to&rdquo; block of water and garbage bills.</p>
      <UiField label="Bank" for="l-bank" optional :error="fields.bank_name"><UiInput id="l-bank" v-model="form.bank_name" :invalid="!!fields.bank_name" /></UiField>
      <UiField label="Account name" for="l-accname" optional :error="fields.bank_account_name"><UiInput id="l-accname" v-model="form.bank_account_name" :invalid="!!fields.bank_account_name" /></UiField>
      <UiField label="Account number" for="l-accno" optional :error="fields.bank_account_number"><UiInput id="l-accno" v-model="form.bank_account_number" :invalid="!!fields.bank_account_number" /></UiField>
    </form>
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton type="submit" form="landlord-form" :loading="loading" :disabled="!form.name || !form.phone" @click="save">Save</UiButton>
    </template>
  </UiModal>
</template>
