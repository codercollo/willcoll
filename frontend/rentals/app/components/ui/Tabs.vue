<script setup lang="ts">
// Tab row with a 2px accent underline over a gray track (design-tokens 7.3).
// Give `to` for route-driven tabs, or v-model for local state.
const model = defineModel<string>()
defineProps<{ tabs: { key: string; label: string; to?: string }[] }>()
const NuxtLink = resolveComponent('NuxtLink')
</script>

<template>
  <nav class="flex h-11 gap-8 overflow-x-auto border-b border-border-default" role="tablist">
    <component
      :is="t.to ? NuxtLink : 'button'"
      v-for="t in tabs"
      :key="t.key"
      :to="t.to"
      role="tab"
      :type="t.to ? undefined : 'button'"
      :aria-selected="model === t.key"
      :class="[
        '-mb-px h-11 border-b-2 px-3 whitespace-nowrap transition-colors duration-(--duration-base)',
        model === t.key ? 'border-accent-primary font-semibold text-gray-900' : 'border-transparent text-gray-500 hover:text-gray-900',
      ]"
      @click="!t.to && (model = t.key)"
    >{{ t.label }}</component>
  </nav>
</template>
