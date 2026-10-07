<script setup lang="ts">
withDefaults(defineProps<{ tone?: 'error' | 'success' | 'info' }>(), { tone: 'error' })
const icons = { error: 'lucide:circle-alert', success: 'lucide:circle-check', info: 'lucide:info' }
const tones = { error: 'bg-arrears-tint text-arrears-text', success: 'bg-paid-tint text-paid-text', info: 'bg-accent-primary-tint text-accent-text' }
</script>

<template>
  <div :class="['flex items-start gap-3 rounded-sm p-4', tones[tone]]" :role="tone === 'error' ? 'alert' : 'status'">
    <Icon :name="icons[tone]" class="mt-0.5 size-5 shrink-0" />
    <div class="flex-1 text-gray-900"><slot /></div>
  </div>
</template>
