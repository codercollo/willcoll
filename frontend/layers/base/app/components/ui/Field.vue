<script setup lang="ts">
// Label is always visible (7.7). `for` must match the control's id.
defineProps<{ label: string; for?: string; help?: string; error?: string; optional?: boolean }>()
</script>

<template>
  <div>
    <label :for="$props.for" class="mb-2 block text-[length:var(--text-label)] leading-[var(--text-label--line-height)] font-medium text-gray-900">
      {{ label }}<span v-if="optional" class="ml-1 font-normal text-gray-500">(optional)</span>
    </label>
    <slot />
    <p v-if="error" class="mt-1 flex items-center gap-1 text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)] text-arrears-text" role="alert">
      <Icon name="lucide:triangle-alert" class="size-[14px] shrink-0" />{{ error }}
    </p>
    <p v-else-if="help" class="mt-1 text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)] text-gray-500">{{ help }}</p>
  </div>
</template>
