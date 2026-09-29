<script setup lang="ts">
withDefaults(defineProps<{ padded?: boolean; compact?: boolean; interactive?: boolean; to?: string }>(), { padded: true })
const NuxtLink = resolveComponent('NuxtLink')
</script>

<template>
  <component
    :is="to ? NuxtLink : 'div'"
    :to="to"
    :class="[
      'card block border border-transparent',
      padded && (compact ? 'p-4' : 'p-6'),
      (interactive || to) && 'transition-[box-shadow,border-color] duration-(--duration-fast) ease-out hover:border-blue-100 hover:shadow-popover',
    ]"
  >
    <slot />
  </component>
</template>
