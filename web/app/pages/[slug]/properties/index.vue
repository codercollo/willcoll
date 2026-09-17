<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

interface Property { id: string; name: string; location: string }

const properties = ref<Property[]>([])
const collectedByProperty = ref<Record<string, number>>({})
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

const showCreate = ref(false)
const form = ref({ name: '', location: '' })
const creating = ref(false)
const createError = ref('')

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: Property[] }>('/v1/properties')
    properties.value = response.data
    status.value = response.data.length ? 'ready' : 'empty'
    loadCollected()
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load properties.'
    status.value = 'error'
  }
}

async function loadCollected() {
  const month = new Date().toISOString().slice(0, 7)
  try {
    const response = await api.request<{ data: Array<{ property_id: string; collected: string | number }> }>(
      `/v1/reports/collections?month=${month}`,
    )
    collectedByProperty.value = Object.fromEntries(response.data.map((row) => [row.property_id, Number(row.collected)]))
  } catch {
    // Metric is supplementary — the property list above already loaded fine.
  }
}

async function createProperty() {
  createError.value = ''
  creating.value = true
  try {
    await api.request('/v1/properties', { method: 'POST', body: form.value })
    showCreate.value = false
    form.value = { name: '', location: '' }
    await load()
  } catch (e: any) {
    createError.value = e?.data?.error ?? 'Unable to create property.'
  } finally {
    creating.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Properties</h1>
      <RoleGate :roles="['manager', 'landlord']">
        <button type="button" class="primary-button" @click="showCreate = !showCreate">
          {{ showCreate ? 'Cancel' : 'Add property' }}
        </button>
      </RoleGate>
    </div>

    <form v-if="showCreate" class="create-form" @submit.prevent="createProperty">
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" type="text" required>
      </label>
      <label class="field">
        <span>Location</span>
        <input v-model="form.location" type="text" required>
      </label>
      <p v-if="createError" class="page-error">{{ createError }}</p>
      <button type="submit" class="primary-button" :disabled="creating">
        {{ creating ? 'Saving...' : 'Save property' }}
      </button>
    </form>

    <p v-if="status === 'loading'" class="page-state">Loading properties&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">No properties yet.</p>

    <section v-else class="property-grid">
      <PropertySwitcherCard
        v-for="property in properties"
        :key="property.id"
        :id="property.id"
        :name="property.name"
        :location="property.location"
        :collected-this-month="collectedByProperty[property.id] ?? null"
      />
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

.page-error { color: var(--color-status-error, #C6362E); }
.page-state { color: var(--color-text-muted, #6B7280); }

.create-form {
  display: grid;
  gap: var(--space-3);
  max-width: 420px;
  padding: var(--space-4);
  margin-bottom: var(--space-6);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.field { display: grid; gap: var(--space-1); }
.field input {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.primary-button {
  padding: var(--space-2) var(--space-4);
  border: 0;
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
  color: #FFFFFF;
  font-weight: 600;
  cursor: pointer;
  justify-self: start;
}
.primary-button:disabled { opacity: 0.7; cursor: default; }

.property-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: var(--space-6);
}
</style>
