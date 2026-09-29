<script setup lang="ts">
const open = defineModel<boolean>({ default: false })
const props = defineProps<{ propertyId: string }>()
const emit = defineEmits<{ saved: [] }>()

const units = useUnits()
const toast = useToast()
const { loading, fields, message, run } = useSubmit()
const code = ref('')
const meter = ref('')

watch(open, (v) => { if (v) { code.value = ''; meter.value = ''; fields.value = {}; message.value = '' } })

async function save() {
  const u = await run(() => units.create(props.propertyId, { unit_code: code.value.trim(), ...(meter.value.trim() ? { meter_number: meter.value.trim() } : {}) }))
  if (u) { toast.success(`Unit ${u.unit_code} added`); emit('saved'); open.value = false }
}
</script>

<template>
  <UiModal v-model="open" title="Add a unit" width="sm">
    <form id="unit-form" class="flex flex-col gap-5" novalidate @submit.prevent="save">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Unit code" for="u-code" help="As written on the door, e.g. G1 or SHOP NO.2" :error="fields.unit_code"><UiInput id="u-code" v-model="code" :invalid="!!fields.unit_code" /></UiField>
      <UiField label="Water meter number" for="u-meter" optional :error="fields.meter_number"><UiInput id="u-meter" v-model="meter" :invalid="!!fields.meter_number" /></UiField>
    </form>
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton type="submit" form="unit-form" :loading="loading" :disabled="!code.trim()" @click="save">Add unit</UiButton>
    </template>
  </UiModal>
</template>
