<script setup lang="ts">
defineOptions({ inheritAttrs: false })
const model = defineModel<string>({ default: '' })
defineProps<{ id?: string; options: { value: string; label: string }[]; invalid?: boolean; disabled?: boolean; placeholder?: string }>()
</script>

<template>
  <div class="relative">
    <select
      :id="id"
      v-model="model"
      :disabled="disabled"
      :aria-invalid="invalid || undefined"
      v-bind="$attrs"
      :class="[
        'h-11 w-full appearance-none rounded-sm border bg-white pr-10 pl-4 outline-none focus:border-accent-primary focus:ring-2 focus:ring-accent-primary-tint disabled:bg-gray-50 disabled:text-gray-500',
        invalid ? 'border-arrears' : 'border-border-default',
      ]"
    >
      <option v-if="placeholder" value="" disabled>{{ placeholder }}</option>
      <option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option>
    </select>
    <Icon name="lucide:chevron-down" class="pointer-events-none absolute top-1/2 right-4 size-4 -translate-y-1/2 text-gray-500" />
  </div>
</template>
