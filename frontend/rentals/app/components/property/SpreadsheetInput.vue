<script setup lang="ts">
// Editable cell: no border at rest, accent ring on focus (design-tokens 7.4).
const model = defineModel<string>({ default: '' })
defineProps<{ label: string; invalid?: boolean; changed?: boolean; placeholder?: string }>()
</script>

<template>
  <input
    v-model="model"
    inputmode="decimal"
    autocomplete="off"
    :aria-label="label"
    :aria-invalid="invalid || undefined"
    :placeholder="placeholder"
    :class="[
      'money h-9 w-24 rounded-sm border border-transparent bg-transparent px-2 text-right font-semibold outline-none transition-shadow duration-(--duration-fast)',
      'hover:border-border-default focus:border-accent-primary focus:bg-surface-card focus:ring-2 focus:ring-accent-primary-tint',
      changed && 'bg-partial-tint',
      invalid && 'border-arrears',
    ]"
  >
</template>
