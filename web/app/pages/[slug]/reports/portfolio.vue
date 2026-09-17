<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

interface PortfolioRow { property_id: string; name: string; units: number }
interface Metadata { page: number; page_size: number; first_page: number; last_page: number; total_count: number }

const search = ref('')
const page = ref(1)
const rows = ref<PortfolioRow[]>([])
const metadata = ref<Metadata | null>(null)
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

// This is the Landlord's primary screen (spec Phase 7.3) — the backend
// already scopes rows to the caller's own owned properties for role='landlord'
// (reports.PortfolioFilters.Role/UserID), so no client-side ownership filter
// is needed here; a Manager sees their whole org's portfolio the same way.
async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const params = new URLSearchParams({ page: String(page.value) })
    if (search.value.trim()) params.set('q', search.value.trim())
    const response = await api.request<{ data: PortfolioRow[]; metadata: Metadata }>(`/v1/reports/portfolio?${params}`)
    rows.value = response.data
    metadata.value = response.metadata
    status.value = rows.value.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load portfolio.'
    status.value = 'error'
  }
}

function onSearchChange() {
  page.value = 1
  load()
}

function goToPage(next: number) {
  page.value = next
  load()
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Portfolio</h1>
      <input v-model="search" type="search" placeholder="Search properties&hellip;" @change="onSearchChange">
    </div>

    <p v-if="status === 'loading'" class="page-state">Loading portfolio&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">No properties in your portfolio yet.</p>

    <template v-else>
      <table class="report-table">
        <thead>
          <tr><th>Property</th><th>Units</th></tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.property_id">
            <td>{{ row.name }}</td>
            <td>{{ row.units }}</td>
          </tr>
        </tbody>
      </table>

      <div v-if="metadata && metadata.last_page > 1" class="pagination">
        <button type="button" :disabled="metadata.page <= metadata.first_page" @click="goToPage(metadata.page - 1)">Previous</button>
        <span>Page {{ metadata.page }} of {{ metadata.last_page }}</span>
        <button type="button" :disabled="metadata.page >= metadata.last_page" @click="goToPage(metadata.page + 1)">Next</button>
      </div>
    </template>
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

input {
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

.pagination {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-4);
}

.pagination button {
  padding: var(--space-1) var(--space-3);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  background: var(--surface-card, #FFFFFF);
  cursor: pointer;
}
.pagination button:disabled { opacity: 0.5; cursor: default; }
</style>
