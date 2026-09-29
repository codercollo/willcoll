<script setup lang="ts">
defineProps<{ title: string; text?: string; icon?: string }>()
</script>

<template>
  <div class="flex flex-col items-center px-6 py-16 text-center">
    <div class="mb-4 flex size-12 items-center justify-center rounded-full bg-gray-100 text-gray-500">
      <Icon :name="icon ?? 'lucide:inbox'" class="size-6" />
    </div>
    <h3>{{ title }}</h3>
    <p v-if="text" class="mt-1 max-w-sm text-gray-500">{{ text }}</p>
    <div v-if="$slots.default" class="mt-6"><slot /></div>
  </div>
</template>
