<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()
const permissions = usePermissions()

interface Property { id: string; name: string }
interface AuditEntry {
  id: string
  property_id: string | null
  actor_id: string | null
  action: string
  entity_type: string
  entity_id: string | null
  created_at: string
}

const properties = ref<Property[]>([])
const propertyID = ref('')
const entries = ref<AuditEntry[]>([])
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

function propertyName(id: string | null) {
  if (!id) return '—'
  return properties.value.find((p) => p.id === id)?.name ?? id
}

async function loadProperties() {
  try {
    const response = await api.request<{ data: Property[] }>('/v1/properties')
    properties.value = response.data
  } catch {
    // Filter dropdown is supplementary — the log below still loads unfiltered.
  }
}

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  if (!permissions.isManager.value) {
    await navigateTo(`/${route.params.slug}/properties`)
    return
  }
  status.value = 'loading'
  try {
    const query = propertyID.value ? `?property_id=${propertyID.value}` : ''
    const response = await api.request<{ data: AuditEntry[] }>(`/v1/audit-log${query}`)
    entries.value = response.data
    status.value = entries.value.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load the audit log.'
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
      <h1 class="page-title">Audit Log</h1>
      <select v-model="propertyID" @change="load">
        <option value="">All properties</option>
        <option v-for="property in properties" :key="property.id" :value="property.id">{{ property.name }}</option>
      </select>
    </div>

    <p v-if="status === 'loading'" class="page-state">Loading audit log&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">No audit entries yet.</p>

    <table v-else class="report-table">
      <thead>
        <tr><th>When</th><th>Action</th><th>Entity</th><th>Property</th></tr>
      </thead>
      <tbody>
        <tr v-for="entry in entries" :key="entry.id">
          <td>{{ new Date(entry.created_at).toLocaleString() }}</td>
          <td>{{ entry.action }}</td>
          <td>{{ entry.entity_type }}</td>
          <td>{{ propertyName(entry.property_id) }}</td>
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
