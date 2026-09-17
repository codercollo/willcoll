<script setup lang="ts">
const props = defineProps<{
  id: string
  name: string
  location: string
  logoUrl?: string | null
  collectedThisMonth?: number | null
}>()

const branding = useBranding()
const route = useRoute()

const logo = computed(() => props.logoUrl ?? branding.value?.logo_url ?? null)
</script>

<template>
  <NuxtLink :to="`/${route.params.slug}/properties/${id}/units`" class="property-card">
    <img v-if="logo" :src="logo" :alt="name" class="property-card__logo">
    <div v-else class="property-card__logo-placeholder" aria-hidden="true" />

    <div class="property-card__body">
      <h2 class="property-card__name">{{ name }}</h2>
      <p class="property-card__location">{{ location }}</p>
      <p v-if="collectedThisMonth != null" class="property-card__collected">
        KES {{ collectedThisMonth.toLocaleString() }} collected this month
      </p>
    </div>
  </NuxtLink>
</template>

<style scoped>
.property-card {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
  text-decoration: none;
  color: inherit;
}

.property-card__logo {
  width: 40px;
  height: 40px;
  object-fit: contain;
}

.property-card__logo-placeholder {
  width: 40px;
  height: 40px;
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
}

.property-card__name {
  margin: 0;
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
}

.property-card__location {
  margin: 0;
  color: var(--color-text-muted, #6B7280);
}

.property-card__collected {
  margin: 0;
  font-size: var(--text-ui-sm, 0.875rem);
  color: var(--color-status-success, #1E8E5A);
}
</style>
