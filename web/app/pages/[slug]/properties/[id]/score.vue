<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

interface Score {
  id: string
  property_id: string
  computed_at: string
  as_of_date: string
  model_version: string
  noi: string
  dscr: string
  score_value: string
  score_band: string
}

// Approved status tokens only (StatusBadge: success/warning/error) — bands
// A/B/C/D map onto those three, never a separate color per band.
function bandTone(band: string): 'success' | 'warning' | 'error' {
  if (band === 'A' || band === 'B') return 'success'
  if (band === 'C') return 'warning'
  return 'error'
}

const propertyID = route.params.id as string

const status = ref<'loading' | 'ready' | 'no-score' | 'error'>('loading')
const error = ref('')
const latest = ref<Score | null>(null)
const history = ref<Score[]>([])

const form = ref({ operating_expenses: '', annual_debt_service: '', other_income: '' })
const requesting = ref(false)
const requestError = ref('')

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const [latestResponse, historyResponse] = await Promise.allSettled([
      api.request<{ data: Score }>(`/v1/properties/${propertyID}/score`),
      api.request<{ data: Score[] }>(`/v1/properties/${propertyID}/score/history`),
    ])
    latest.value = latestResponse.status === 'fulfilled' ? latestResponse.value.data : null
    history.value = historyResponse.status === 'fulfilled' ? historyResponse.value.data : []
    status.value = latest.value ? 'ready' : 'no-score'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load the property score.'
    status.value = 'error'
  }
}

async function requestScore() {
  requestError.value = ''
  requesting.value = true
  try {
    await api.request(`/v1/properties/${propertyID}/score`, {
      method: 'POST',
      body: {
        operating_expenses: Number(form.value.operating_expenses),
        annual_debt_service: Number(form.value.annual_debt_service),
        other_income: Number(form.value.other_income || 0),
      },
    })
    form.value = { operating_expenses: '', annual_debt_service: '', other_income: '' }
    await load()
  } catch (e: any) {
    requestError.value = e?.data?.error ?? 'Unable to request a score.'
  } finally {
    requesting.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Verified Property Score</h1>
    </div>

    <p v-if="status === 'loading'" class="page-state">Loading&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>

    <template v-else>
      <div v-if="latest" class="score-card">
        <div class="score-headline">
          <StatusBadge :tone="bandTone(latest.score_band)" :label="`Band ${latest.score_band}`" />
          <span class="score-value">{{ latest.score_value }}</span>
        </div>
        <dl class="score-metrics">
          <div><dt>NOI</dt><dd>{{ latest.noi }}</dd></div>
          <div><dt>DSCR</dt><dd>{{ latest.dscr }}</dd></div>
          <div><dt>As of</dt><dd>{{ latest.as_of_date }}</dd></div>
        </dl>
      </div>
      <p v-else class="page-state">No score has been computed for this property yet.</p>

      <!-- Landlord is read-only here — never a "request score" button (spec §13.4). -->
      <RoleGate :roles="['manager']">
        <form class="request-form" @submit.prevent="requestScore">
          <h2 class="section-title">Request a new score</h2>
          <label class="field">
            <span>Operating expenses (KES)</span>
            <input v-model="form.operating_expenses" type="number" step="0.01" required>
          </label>
          <label class="field">
            <span>Annual debt service (KES)</span>
            <input v-model="form.annual_debt_service" type="number" step="0.01" required>
          </label>
          <label class="field">
            <span>Other income (KES)</span>
            <input v-model="form.other_income" type="number" step="0.01">
          </label>
          <p v-if="requestError" class="page-error">{{ requestError }}</p>
          <button type="submit" class="primary-button" :disabled="requesting">
            {{ requesting ? 'Requesting...' : 'Request score' }}
          </button>
        </form>
      </RoleGate>

      <section v-if="history.length" class="history">
        <h2 class="section-title">History</h2>
        <table class="history-table">
          <thead>
            <tr><th>As of</th><th>Band</th><th>Score</th><th>NOI</th><th>DSCR</th></tr>
          </thead>
          <tbody>
            <tr v-for="sc in history" :key="sc.id">
              <td>{{ sc.as_of_date }}</td>
              <td><StatusBadge :tone="bandTone(sc.score_band)" :label="sc.score_band" /></td>
              <td>{{ sc.score_value }}</td>
              <td>{{ sc.noi }}</td>
              <td>{{ sc.dscr }}</td>
            </tr>
          </tbody>
        </table>
      </section>
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

.section-title {
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
  margin: var(--space-6) 0 var(--space-3);
}

.score-card {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.score-headline {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.score-value { font-size: 1.5rem; font-weight: 600; }

.score-metrics {
  display: flex;
  gap: var(--space-6);
  margin: 0;
}
.score-metrics dt { color: var(--color-text-muted, #6B7280); font-size: 0.8125rem; }
.score-metrics dd { margin: 0; font-weight: 600; }

.request-form {
  display: grid;
  gap: var(--space-3);
  max-width: 420px;
  padding: var(--space-4);
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

.history-table {
  width: 100%;
  border-collapse: collapse;
}
.history-table th, .history-table td {
  text-align: left;
  padding: var(--space-2) var(--space-3);
  border-bottom: var(--border-hairline, 1px solid #E2E5E9);
}
</style>
