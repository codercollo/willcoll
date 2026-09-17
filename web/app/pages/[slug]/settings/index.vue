<script setup lang="ts">
definePageMeta({ layout: 'default' })

const route = useRoute()
const base = computed(() => `/${route.params.slug}/settings`)

const links = [
  { label: 'Branding', to: 'branding', description: 'Brand name, accent color, and logo.' },
  { label: 'SMS Templates', to: 'sms-templates', description: 'Edit the tenant and landlord SMS templates.' },
  { label: 'Verified Property Score', to: 'score-addon', description: 'Activate or cancel the score add-on.' },
]
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Settings</h1>
    </div>

    <section class="settings-grid">
      <NuxtLink v-for="link in links" :key="link.to" :to="`${base}/${link.to}`" class="settings-card">
        <h3>{{ link.label }}</h3>
        <p>{{ link.description }}</p>
      </NuxtLink>
    </section>
  </div>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-6);
}

.page-title {
  margin: 0;
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
}

.settings-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: var(--space-4);
}

.settings-card {
  display: block;
  padding: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
  color: var(--color-text-primary, #1F2320);
  text-decoration: none;
}

.settings-card h3 { margin: 0 0 var(--space-1); }
.settings-card p { margin: 0; color: var(--color-text-muted, #6B7280); }
</style>
