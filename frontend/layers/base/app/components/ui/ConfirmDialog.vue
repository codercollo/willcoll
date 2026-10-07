<script setup lang="ts">
const open = defineModel<boolean>({ default: false })
defineProps<{ title: string; text?: string; confirmLabel?: string; destructive?: boolean; loading?: boolean }>()
defineEmits<{ confirm: [] }>()
</script>

<template>
  <UiModal v-model="open" :title="title" width="sm">
    <p v-if="text" class="text-gray-700">{{ text }}</p>
    <slot />
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton :variant="destructive ? 'destructive' : 'primary'" :loading="loading" @click="$emit('confirm')">{{ confirmLabel ?? 'Confirm' }}</UiButton>
    </template>
  </UiModal>
</template>
