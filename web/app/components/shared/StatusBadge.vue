<script setup lang="ts">
const props = defineProps<{
  tone?: 'success' | 'warning' | 'error'
  label: string
}>()

const tone = computed(() => props.tone ?? 'warning')
</script>

<template>
  <span class="status-badge" :class="`status-badge--${tone}`">
    <span class="status-badge__dot" aria-hidden="true" />
    {{ label }}
  </span>
</template>

<style scoped>
.status-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm, 4px);
  font-size: var(--text-ui-xs, 0.75rem);
  font-weight: 500;
}

.status-badge--success { color: var(--color-status-success, #1E8E5A); background: var(--color-status-success-bg, rgba(30,142,90,0.12)); }
.status-badge--warning { color: var(--color-status-warning, #C9821A); background: var(--color-status-warning-bg, rgba(201,130,26,0.12)); }
.status-badge--error   { color: var(--color-status-error, #C6362E); background: var(--color-status-error-bg, rgba(198,54,46,0.12)); }

.status-badge__dot {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: currentColor;
}
</style>
