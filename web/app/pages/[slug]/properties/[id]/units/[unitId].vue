<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

const propertyID = computed(() => String(route.params.id))
const unitID = computed(() => String(route.params.unitId))

interface StatementRow {
  unit_id: string
  period_month: string
  rent_due: string | number
  rent_paid: string | number
  water_due: string | number
  water_paid: string | number
}
interface Metadata { page: number; page_size: number; first_page: number; last_page: number; total_count: number }

const rows = ref<StatementRow[]>([])
const meta = ref<Metadata | null>(null)
const page = ref(1)
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

async function loadStatement() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: StatementRow[]; metadata: Metadata }>(
      `/v1/units/${unitID.value}/statement?page=${page.value}`,
    )
    rows.value = response.data
    meta.value = response.metadata
    status.value = response.data.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load the unit statement.'
    status.value = 'error'
  }
}

// Create lease
const showLeaseForm = ref(false)
const leaseForm = ref({ full_name: '', phone: '', id_number: '', monthly_rent: 0, deposit_paid: 0, start_date: '', rent_due_day: 5 })
const leaseCreating = ref(false)
const leaseError = ref('')

async function createLease() {
  leaseError.value = ''
  leaseCreating.value = true
  try {
    await api.request(`/v1/units/${unitID.value}/leases`, { method: 'POST', body: leaseForm.value })
    showLeaseForm.value = false
    await loadStatement()
  } catch (e: any) {
    leaseError.value = e?.data?.error ?? 'Unable to create lease.'
  } finally {
    leaseCreating.value = false
  }
}

// Terminate lease — no endpoint yet returns "the unit's current lease id",
// so this asks for it directly rather than guessing one. Flagged, not hidden.
const showTerminateForm = ref(false)
const terminateForm = ref({ lease_id: '', reason: '' })
const terminating = ref(false)
const terminateError = ref('')

async function terminateLease() {
  terminateError.value = ''
  terminating.value = true
  try {
    await api.request(`/v1/leases/${terminateForm.value.lease_id}/terminate`, {
      method: 'POST',
      body: { reason: terminateForm.value.reason },
    })
    showTerminateForm.value = false
    await loadStatement()
  } catch (e: any) {
    terminateError.value = e?.data?.error ?? 'Unable to terminate lease.'
  } finally {
    terminating.value = false
  }
}

watch(page, loadStatement)
onMounted(loadStatement)

const showPaymentDrawer = ref(false)
</script>

<template>
  <div class="unit-page">
    <div class="page-header">
      <h1 class="page-title">Unit statement</h1>
      <div class="page-actions">
        <RoleGate action="record_payment">
          <button type="button" class="secondary-button" @click="showPaymentDrawer = true">
            Record Payment
          </button>
        </RoleGate>
        <RoleGate action="edit_lease">
          <button type="button" class="secondary-button" @click="showLeaseForm = !showLeaseForm">
            {{ showLeaseForm ? 'Cancel' : 'New lease' }}
          </button>
          <button type="button" class="secondary-button" @click="showTerminateForm = !showTerminateForm">
            {{ showTerminateForm ? 'Cancel' : 'Terminate lease' }}
          </button>
        </RoleGate>
      </div>
    </div>

    <form v-if="showLeaseForm" class="inline-form" @submit.prevent="createLease">
      <label class="field"><span>Tenant name</span><input v-model="leaseForm.full_name" type="text" required></label>
      <label class="field"><span>Phone</span><input v-model="leaseForm.phone" type="text" required></label>
      <label class="field"><span>ID number</span><input v-model="leaseForm.id_number" type="text"></label>
      <label class="field"><span>Monthly rent (KES)</span><input v-model.number="leaseForm.monthly_rent" type="number" min="0" required></label>
      <label class="field"><span>Deposit (KES)</span><input v-model.number="leaseForm.deposit_paid" type="number" min="0" required></label>
      <label class="field"><span>Start date</span><input v-model="leaseForm.start_date" type="date" required></label>
      <label class="field"><span>Rent due day</span><input v-model.number="leaseForm.rent_due_day" type="number" min="1" max="28" required></label>
      <p v-if="leaseError" class="page-error">{{ leaseError }}</p>
      <button type="submit" class="primary-button" :disabled="leaseCreating">{{ leaseCreating ? 'Saving...' : 'Move in' }}</button>
    </form>

    <form v-if="showTerminateForm" class="inline-form" @submit.prevent="terminateLease">
      <p class="form-note">No endpoint yet exposes this unit's active lease id directly — enter it manually.</p>
      <label class="field"><span>Lease ID</span><input v-model="terminateForm.lease_id" type="text" required></label>
      <label class="field"><span>Reason</span><input v-model="terminateForm.reason" type="text"></label>
      <p v-if="terminateError" class="page-error">{{ terminateError }}</p>
      <button type="submit" class="primary-button" :disabled="terminating">{{ terminating ? 'Terminating...' : 'Terminate' }}</button>
    </form>

    <p v-if="status === 'loading'" class="page-state">Loading statement&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">No billing history for this unit yet.</p>

    <template v-else>
      <LedgerTimeline :rows="rows" />

      <div v-if="meta && meta.last_page > 1" class="pagination">
        <button type="button" :disabled="page <= 1" @click="page--">Previous</button>
        <span>Page {{ meta.page }} of {{ meta.last_page }}</span>
        <button type="button" :disabled="page >= meta.last_page" @click="page++">Next</button>
      </div>
    </template>

    <PaymentDrawer
      :unit-id="unitID"
      :open="showPaymentDrawer"
      @close="showPaymentDrawer = false"
      @success="loadStatement"
    />
  </div>
</template>

<style scoped>
.unit-page { max-width: 640px; }

.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-3);
  margin-bottom: var(--space-6);
}

.page-title { margin: 0; font-family: var(--font-display, serif); color: var(--color-text-primary, #1F2320); }
.page-actions { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.page-error { color: var(--color-status-error, #C6362E); }
.page-state { color: var(--color-text-muted, #6B7280); }

.inline-form {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  margin-bottom: var(--space-6);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.form-note { margin: 0; font-size: var(--text-ui-sm); color: var(--color-text-muted, #6B7280); }
.field { display: grid; gap: var(--space-1); }
.field input {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.primary-button, .secondary-button {
  padding: var(--space-2) var(--space-4);
  border-radius: var(--radius-md, 6px);
  font-weight: 600;
  cursor: pointer;
  justify-self: start;
}
.primary-button { border: 0; background: var(--color-action-primary, #E8702A); color: #FFFFFF; }
.secondary-button { border: var(--border-hairline, 1px solid #E2E5E9); background: var(--surface-card, #FFFFFF); color: var(--color-text-primary, #1F2320); }
.primary-button:disabled, .secondary-button:disabled { opacity: 0.6; cursor: default; }

.pagination {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-4);
  color: var(--color-text-muted, #6B7280);
}
.pagination button {
  padding: var(--space-1) var(--space-3);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  background: var(--surface-card, #FFFFFF);
  cursor: pointer;
}
.pagination button:disabled { opacity: 0.5; cursor: default; }

@media (max-width: 640px) {
  .page-actions { width: 100%; }
  .page-actions button { flex: 1; }
}
</style>
