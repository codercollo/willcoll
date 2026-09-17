<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

const propertyID = computed(() => String(route.params.id))

interface Meter {
  id: string
  unit_id: string
  unit_label: string
  meter_type: string
  meter_number: string | null
  latest_reading: { reading_month: string; current_reading: string | number } | null
}

const meters = ref<Meter[]>([])
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

// One draft reading per meter, keyed by meter id.
const drafts = ref<Record<string, { reading_month: string; current_reading: number | null }>>({})
const saving = ref<Record<string, boolean>>({})
const rowError = ref<Record<string, string>>({})

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: Meter[] }>(`/v1/properties/${propertyID.value}/meters`)
    meters.value = response.data
    for (const m of response.data) {
      drafts.value[m.id] ??= { reading_month: new Date().toISOString().slice(0, 8) + '01', current_reading: null }
    }
    status.value = response.data.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load meters.'
    status.value = 'error'
  }
}

// Mirrors validate.go's server-side monotonicity rule (current >= previous)
// as an early hint only — the actual error shown on submit always comes
// from the server's response, never this client-side guess alone.
function belowPrevious(meter: Meter) {
  const draft = drafts.value[meter.id]
  if (!meter.latest_reading || draft?.current_reading == null) return false
  return Number(draft.current_reading) < Number(meter.latest_reading.current_reading)
}

async function submitReading(meter: Meter) {
  const draft = drafts.value[meter.id]
  if (!draft || draft.current_reading == null) return
  rowError.value[meter.id] = ''
  saving.value[meter.id] = true
  try {
    const response = await api.request<{ data: { invoice_id: string } }>(`/v1/meters/${meter.id}/readings`, {
      method: 'POST',
      body: { reading_month: draft.reading_month, current_reading: draft.current_reading },
    })
    // Navigates to the unit's statement so the manager sees the invoice this
    // reading just generated (there is no standalone single-invoice page).
    await navigateTo(`/${route.params.slug}/properties/${propertyID.value}/units/${meter.unit_id}?invoice=${response.data.invoice_id}`)
  } catch (e: any) {
    rowError.value[meter.id] = e?.data?.error ?? 'Unable to record this reading.'
  } finally {
    saving.value[meter.id] = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <h1 class="page-title">Meter readings</h1>

    <p v-if="status === 'loading'" class="page-state">Loading meters&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">
      No meters yet. Add one from a unit before recording readings.
    </p>

    <section v-else class="meter-grid">
      <div v-for="meter in meters" :key="meter.id" class="meter-row">
        <div class="meter-row__unit">{{ meter.unit_label }}</div>
        <div class="meter-row__previous">
          Previous: {{ meter.latest_reading ? Number(meter.latest_reading.current_reading).toLocaleString() : '—' }}
        </div>
        <input
          v-model.number="drafts[meter.id].current_reading"
          type="number"
          min="0"
          class="meter-row__input"
          placeholder="Current reading"
        >
        <button
          type="button"
          class="primary-button"
          :disabled="saving[meter.id] || drafts[meter.id].current_reading == null"
          @click="submitReading(meter)"
        >
          {{ saving[meter.id] ? 'Saving...' : 'Save' }}
        </button>
        <p v-if="belowPrevious(meter)" class="hint-warning">Below the previous reading — the server will reject this.</p>
        <p v-if="rowError[meter.id]" class="page-error">{{ rowError[meter.id] }}</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.page-title { margin: 0 0 var(--space-6); font-family: var(--font-display, serif); color: var(--color-text-primary, #1F2320); }
.page-error { color: var(--color-status-error, #C6362E); }
.page-state { color: var(--color-text-muted, #6B7280); }

.meter-grid { display: flex; flex-direction: column; gap: var(--space-3); }

.meter-row {
  display: grid;
  grid-template-columns: 1fr 1fr auto auto;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--surface-panel, #FFFFFF);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.meter-row__unit { font-weight: 600; }
.meter-row__previous { color: var(--color-text-muted, #6B7280); font-size: var(--text-ui-sm); }
.meter-row__input {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  width: 140px;
}

.hint-warning {
  grid-column: 1 / -1;
  margin: 0;
  color: var(--color-status-warning, #C9821A);
  font-size: var(--text-ui-sm);
}

.primary-button {
  padding: var(--space-2) var(--space-4);
  border: 0;
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
  color: #FFFFFF;
  font-weight: 600;
  cursor: pointer;
}
.primary-button:disabled { opacity: 0.6; cursor: default; }

@media (max-width: 640px) {
  .meter-row { grid-template-columns: 1fr; }
  .meter-row__input { width: 100%; }
}
</style>
