<script setup lang="ts">
withDefaults(defineProps<{ lines?: number; block?: boolean }>(), { lines: 1 })
</script>

<template>
  <div v-if="block" class="h-24 animate-pulse rounded-md bg-gray-100" aria-hidden="true" />
  <div v-else class="space-y-2" role="status" aria-label="Loading">
    <div v-for="i in lines" :key="i" class="h-4 animate-pulse rounded-sm bg-gray-100" :class="i === lines && lines > 1 ? 'w-2/3' : 'w-full'" />
  </div>
</template>
