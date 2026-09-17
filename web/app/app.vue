<script setup lang="ts">
// Phase 0 gate: a blank page fetches GET /v1/public/branding (via
// plugins/branding.client.ts, which runs before mount) and paints the real
// name/color here — no mock data, three real states.
const branding = useBranding()
const status = useBrandingStatus()
</script>

<template>
  <div>
    <NuxtRouteAnnouncer />
    <div v-if="status === 'loading'" class="gate-state">Loading&hellip;</div>
    <div v-else-if="status === 'error'" class="gate-state gate-state--error">
      Could not load organization branding.
    </div>
    <div v-else class="gate-state">
      <span class="gate-brand">{{ branding?.brand_name }}</span>
    </div>
    <NuxtPage />
  </div>
</template>

<style scoped>
.gate-state {
  padding: var(--space-4);
  font-family: var(--font-ui);
  color: var(--color-text-primary);
}
.gate-state--error {
  color: var(--color-status-error);
}
.gate-brand {
  font-family: var(--font-display);
  color: var(--color-action-primary, var(--color-text-primary));
}
</style>
