<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()

const propertyID = computed(() => String(route.params.id))

interface Landlord { landlord_id: string; full_name: string; phone: string }

const landlords = ref<Landlord[]>([])
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

const form = ref({ landlord_id: '', amount: 0, method: 'bank', reference: '', narrative: '' })
const submitting = ref(false)
const submitError = ref('')
const submitted = ref(false)

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: Landlord[] }>(`/v1/properties/${propertyID.value}/landlords`)
    landlords.value = response.data
    if (response.data.length === 1) form.value.landlord_id = response.data[0].landlord_id
    status.value = response.data.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load landlords for this property.'
    status.value = 'error'
  }
}

async function submit() {
  submitError.value = ''
  submitted.value = false
  submitting.value = true
  try {
    await api.request('/v1/remittances', {
      method: 'POST',
      body: {
        property_id: propertyID.value,
        landlord_id: form.value.landlord_id,
        amount: Math.round(form.value.amount * 100), // KES -> cents
        method: form.value.method,
        reference: form.value.reference,
        narrative: form.value.narrative,
        idempotency_key: `remit-${propertyID.value}-${Date.now()}`,
      },
    })
    submitted.value = true
    form.value.amount = 0
    form.value.reference = ''
    form.value.narrative = ''
  } catch (e: any) {
    submitError.value = e?.data?.error ?? 'Unable to record this remittance.'
  } finally {
    submitting.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="remittance-page">
    <h1 class="page-title">Remit to landlord</h1>

    <p v-if="status === 'loading'" class="page-state">Loading landlords&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">
      No landlord is attached to this property yet.
    </p>

    <form v-else class="remittance-form" @submit.prevent="submit">
      <label class="field">
        <span>Landlord</span>
        <select v-model="form.landlord_id" required>
          <option value="" disabled>Select a landlord</option>
          <option v-for="l in landlords" :key="l.landlord_id" :value="l.landlord_id">{{ l.full_name }}</option>
        </select>
      </label>
      <label class="field"><span>Amount (KES)</span><input v-model.number="form.amount" type="number" min="1" step="0.01" required></label>
      <label class="field">
        <span>Method</span>
        <select v-model="form.method">
          <option value="bank">Bank</option>
          <option value="cash">Cash</option>
          <option value="mpesa_manual">M-Pesa</option>
          <option value="cheque">Cheque</option>
        </select>
      </label>
      <label class="field"><span>Reference</span><input v-model="form.reference" type="text"></label>
      <label class="field"><span>Narrative (optional)</span><input v-model="form.narrative" type="text"></label>
      <p v-if="submitError" class="page-error">{{ submitError }}</p>
      <p v-if="submitted" class="page-success">Remittance recorded.</p>
      <button type="submit" class="primary-button" :disabled="submitting">
        {{ submitting ? 'Recording...' : 'Record remittance' }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.remittance-page { max-width: 420px; }
.page-title { margin: 0 0 var(--space-6); font-family: var(--font-display, serif); color: var(--color-text-primary, #1F2320); }
.page-error { color: var(--color-status-error, #C6362E); }
.page-success { color: var(--color-status-success, #1E8E5A); }
.page-state { color: var(--color-text-muted, #6B7280); }

.remittance-form {
  display: grid;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.field { display: grid; gap: var(--space-1); }
.field input, .field select {
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
.primary-button:disabled { opacity: 0.6; cursor: default; }
</style>
