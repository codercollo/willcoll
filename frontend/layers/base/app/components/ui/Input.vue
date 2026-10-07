<script setup lang="ts">
// Text input; `money` gives the right-aligned tabular "Ksh" amount variant.
// `prefix` overrides the leading label text (e.g. "units" for a meter
// reading), keeping the same numeric/right-aligned layout as `money`.
defineOptions({ inheritAttrs: false })
const model = defineModel<string>({ default: '' })
defineProps<{ id?: string; type?: string; placeholder?: string; invalid?: boolean; money?: boolean; prefix?: string; disabled?: boolean; autocomplete?: string; inputmode?: 'text' | 'numeric' | 'decimal' | 'tel' | 'email' }>()
</script>

<template>
  <div
    :class="[
      'flex h-11 items-center rounded-sm border bg-white transition-shadow duration-(--duration-fast) focus-within:border-accent-primary focus-within:ring-2 focus-within:ring-accent-primary-tint',
      invalid ? 'border-arrears' : 'border-border-default',
      disabled && 'bg-gray-50 text-gray-500',
    ]"
  >
    <span v-if="money || prefix" class="pl-4 text-gray-500 select-none">{{ prefix ?? 'Ksh' }}</span>
    <input
      :id="id"
      v-model="model"
      :type="type ?? 'text'"
      :placeholder="placeholder"
      :disabled="disabled"
      :autocomplete="autocomplete"
      :inputmode="money ? 'decimal' : inputmode"
      :aria-invalid="invalid || undefined"
      v-bind="$attrs"
      :class="['h-full w-0 min-w-0 flex-1 bg-transparent px-4 outline-none placeholder:text-gray-500', money && 'money text-right font-semibold']"
    >
    <slot name="suffix" />
  </div>
</template>
