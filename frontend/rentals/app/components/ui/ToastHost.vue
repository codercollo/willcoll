<script setup lang="ts">
const { items, dismiss } = useToast()
const icons = { success: 'lucide:circle-check', error: 'lucide:circle-alert', info: 'lucide:info' }
const tones = { success: 'text-paid-text', error: 'text-arrears-text', info: 'text-accent-text' }
</script>

<template>
  <div class="pointer-events-none fixed right-4 bottom-4 z-[60] flex w-[calc(100%-2rem)] max-w-sm flex-col gap-3" aria-live="polite">
    <div v-for="t in items" :key="t.id" class="pointer-events-auto flex items-start gap-3 rounded-md bg-white p-4 shadow-popover" role="status">
      <Icon :name="icons[t.kind]" :class="['mt-0.5 size-5 shrink-0', tones[t.kind]]" />
      <p class="flex-1">{{ t.message }}</p>
      <UiButton variant="icon" label="Dismiss" @click="dismiss(t.id)"><Icon name="lucide:x" class="size-4" /></UiButton>
    </div>
  </div>
</template>
