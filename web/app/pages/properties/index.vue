<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const properties = ref<Array<{
  id: string
  name: string
  location: string
}>>([])
const error = ref('')

async function load() {
  if (!auth.token.value) {
    await navigateTo('/login')
    return
  }
  try {
    const response = await $fetch<{ data: Array<{ id: string; name: string; location: string }> }>('/v1/properties', {
      headers: { Authorization: `Bearer ${auth.token.value}` },
    })
    properties.value = response.data
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load properties.'
  }
}

onMounted(load)
</script>

<template>
  <div>
    <h1 class="page-title">Properties</h1>
    <p v-if="error" class="page-error">{{ error }}</p>

    <section class="property-grid">
      <PropertySwitcherCard
        v-for="property in properties"
        :key="property.id"
        :id="property.id"
        :name="property.name"
        :location="property.location"
      />
    </section>
  </div>
</template>

<style scoped>
.page-title {
  margin: 0 0 var(--space-8);
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
}

.page-error { color: var(--color-status-error, #C6362E); }

.property-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: var(--spacing-lg, 24px);
}
</style>
