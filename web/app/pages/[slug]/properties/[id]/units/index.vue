<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

interface Unit { id: string; unit_label: string; status: string; base_rent: number }

const propertyID = computed(() => String(route.params.id))
const units = ref<Unit[]>([])
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

const showCreate = ref(false)
const form = ref({ unit_label: '', unit_type: '', base_rent: 0, deposit_amount: 0 })
const creating = ref(false)
const createError = ref('')

const importFile = ref<File | null>(null)
const importing = ref(false)
const importError = ref('')
const importResult = ref<{ created_count: number; skipped_count: number } | null>(null)

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: Unit[] }>(`/v1/properties/${propertyID.value}/units`)
    units.value = response.data
    status.value = response.data.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load units.'
    status.value = 'error'
  }
}

async function createUnit() {
  createError.value = ''
  creating.value = true
  try {
    await api.request(`/v1/properties/${propertyID.value}/units`, { method: 'POST', body: form.value })
    showCreate.value = false
    form.value = { unit_label: '', unit_type: '', base_rent: 0, deposit_amount: 0 }
    await load()
  } catch (e: any) {
    createError.value = e?.data?.error ?? 'Unable to create unit.'
  } finally {
    creating.value = false
  }
}

function onFileChange(e: Event) {
  importFile.value = (e.target as HTMLInputElement).files?.[0] ?? null
}

async function importTenants() {
  if (!importFile.value) return
  importError.value = ''
  importResult.value = null
  importing.value = true
  try {
    const body = new FormData()
    body.append('file', importFile.value)
    const response = await api.request<{ data: { created_count: number; skipped_count: number } }>(
      `/v1/properties/${propertyID.value}/tenants/import`,
      { method: 'POST', body },
    )
    importResult.value = response.data
    await load()
  } catch (e: any) {
    // The backend's real 409 ("attach a landlord to this property before
    // importing tenants") is surfaced as-is, not a generic failure message.
    importError.value = e?.data?.error ?? 'Import failed.'
  } finally {
    importing.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Units</h1>
      <div class="page-actions">
        <RoleGate action="record_meter_reading">
          <NuxtLink :to="`/${route.params.slug}/properties/${propertyID}/meters`" class="secondary-button">
            Meter readings
          </NuxtLink>
        </RoleGate>
        <RoleGate :roles="['manager']">
          <NuxtLink :to="`/${route.params.slug}/properties/${propertyID}/remittances`" class="secondary-button">
            Remit to landlord
          </NuxtLink>
        </RoleGate>
        <RoleGate :roles="['manager']">
          <button type="button" class="primary-button" @click="showCreate = !showCreate">
            {{ showCreate ? 'Cancel' : 'Add unit' }}
          </button>
        </RoleGate>
      </div>
    </div>

    <form v-if="showCreate" class="create-form" @submit.prevent="createUnit">
      <label class="field"><span>Unit label</span><input v-model="form.unit_label" type="text" required></label>
      <label class="field"><span>Unit type</span><input v-model="form.unit_type" type="text" placeholder="e.g. bedsitter"></label>
      <label class="field"><span>Base rent (KES)</span><input v-model.number="form.base_rent" type="number" min="0" required></label>
      <label class="field"><span>Deposit (KES)</span><input v-model.number="form.deposit_amount" type="number" min="0" required></label>
      <p v-if="createError" class="page-error">{{ createError }}</p>
      <button type="submit" class="primary-button" :disabled="creating">{{ creating ? 'Saving...' : 'Save unit' }}</button>
    </form>

    <RoleGate :roles="['manager']">
      <div class="import-panel">
        <h2 class="import-title">Bulk import units + tenants (CSV)</h2>
        <div class="import-controls">
          <input type="file" accept=".csv" @change="onFileChange">
          <button type="button" class="primary-button" :disabled="!importFile || importing" @click="importTenants">
            {{ importing ? 'Importing...' : 'Import' }}
          </button>
        </div>
        <p v-if="importError" class="page-error">{{ importError }}</p>
        <p v-if="importResult" class="import-result">
          {{ importResult.created_count }} created, {{ importResult.skipped_count }} skipped.
        </p>
      </div>
    </RoleGate>

    <p v-if="status === 'loading'" class="page-state">Loading units&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">No units yet.</p>

    <section v-else class="unit-list">
      <NuxtLink
        v-for="unit in units"
        :key="unit.id"
        :to="`/${route.params.slug}/properties/${propertyID}/units/${unit.id}`"
        class="unit-row"
      >
        <div class="unit-row__label">{{ unit.unit_label }}</div>
        <StatusBadge :label="unit.status" :tone="unit.status === 'occupied' ? 'success' : 'warning'" />
        <div class="unit-row__balance">KES {{ unit.base_rent.toLocaleString() }}</div>
      </NuxtLink>
    </section>
  </div>
</template>

<style scoped>
.page-header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); margin-bottom: var(--space-6); }
.page-title { margin: 0; font-family: var(--font-display, serif); color: var(--color-text-primary, #1F2320); }
.page-error { color: var(--color-status-error, #C6362E); }
.page-state { color: var(--color-text-muted, #6B7280); }

.create-form, .import-panel {
  display: grid;
  gap: var(--space-3);
  max-width: 420px;
  padding: var(--space-4);
  margin-bottom: var(--space-6);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.import-title { margin: 0; font-size: var(--text-ui-base); color: var(--color-text-primary, #1F2320); }
.import-controls { display: flex; align-items: center; gap: var(--space-3); flex-wrap: wrap; }
.import-result { color: var(--color-status-success, #1E8E5A); }

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

.page-actions { display: flex; align-items: center; gap: var(--space-3); }
.secondary-button {
  padding: var(--space-2) var(--space-4);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-md, 6px);
  background: var(--surface-card, #FFFFFF);
  color: var(--color-text-primary, #1F2320);
  text-decoration: none;
  font-weight: 600;
}

.unit-list { display: flex; flex-direction: column; gap: var(--space-2); }

.unit-row {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  background: var(--surface-panel, #FFFFFF);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  text-decoration: none;
  color: var(--color-text-primary, #1F2320);
}

.unit-row__label { font-weight: 600; }
.unit-row__balance { margin-left: auto; font-variant-numeric: tabular-nums; }
</style>
