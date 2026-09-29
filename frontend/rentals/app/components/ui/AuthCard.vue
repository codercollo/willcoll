<script setup lang="ts">
defineProps<{ title: string; subtitle?: string }>()
</script>

<template>
  <div class="w-full max-w-[440px] rounded-lg bg-white p-8 shadow-popover">
    <h1 class="text-[length:var(--text-h1)]">{{ title }}</h1>
    <p v-if="subtitle" class="mt-1 text-gray-500">{{ subtitle }}</p>
    <div class="mt-6"><slot /></div>
    <div v-if="$slots.footer" class="mt-6 border-t border-border-subtle pt-6 text-center text-gray-500"><slot name="footer" /></div>
  </div>
</template>
