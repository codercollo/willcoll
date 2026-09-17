<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

interface Property { id: string; name: string }
interface ArrearsRow { unit_id: string; property_id: string; unit_label: string; arrears: string }

const properties = ref<Property[]>([])
const propertyID = ref('')
const rows = ref<ArrearsRow[]>([])
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

async function loadProperties() {
  try {
    const response = await api.request<{ data: Property[] }>('/v1/properties')
    properties.value = response.data
  } catch {
    // Filter dropdown is supplementary — the report below still loads unfiltered.
  }
}

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const query = propertyID.value ? `?property_id=${propertyID.value}` : ''
    const response = await api.request<{ data: ArrearsRow[] }>(`/v1/reports/arrears${query}`)
    rows.value = response.data
    status.value = rows.value.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load arrears.'
    status.value = 'error'
  }
}

onMounted(async () => {
  await loadProperties()
  await load()
})
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Arrears</h1>
      <select v-model="propertyID" @change="load">
        <option value="">All properties</option>
        <option v-for="property in properties" :key="property.id" :value="property.id">{{ property.name }}</option>
      </select>
    </div>

    <p v-if="status === 'loading'" class="page-state">Loading arrears&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">No arrears outstanding.</p>

    <table v-else class="report-table">
      <thead>
        <tr><th>Unit</th><th>Arrears (KES)</th></tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.unit_id">
          <td>{{ row.unit_label }}</td>
          <td>{{ row.arrears }}</td>
        </tr>
      </tbody>
    </table>
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

select {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.report-table {
  width: 100%;
  border-collapse: collapse;
}

.report-table th, .report-table td {
  text-align: left;
  padding: var(--space-2) var(--space-3);
  border-bottom: var(--border-hairline, 1px solid #E2E5E9);
}
</style>
