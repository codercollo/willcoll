<script setup lang="ts">
const props = defineProps<{ unitId: string; open: boolean }>()
const emit = defineEmits<{ close: []; success: [] }>()

const api = useApi()

interface StatementRow {
  period_month: string
  invoice_type?: string
  rent_due: string | number
  rent_paid: string | number
  water_due: string | number
  water_paid: string | number
}
interface AllocationLeg {
  invoice_id: string
  invoice_type: string
  period_month: string
  applied: number
  transfer_id: string
}
interface PaymentResult {
  total_amount: number
  allocations: AllocationLeg[]
  unallocated_amount: number
}

const openRows = ref<{ label: string; due: number; paid: number }[]>([])
const openStatus = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')

const amount = ref<number | null>(null)
const method = ref<'cash' | 'mpesa' | 'bank' | 'card'>('cash')
const referenceNumber = ref('')
const notes = ref('')

const submitting = ref(false)
const submitError = ref('')
const result = ref<PaymentResult | null>(null)

// The unit's open invoices, fetched live from the real statement endpoint —
// never computed client-side. This is display only; the actual allocation
// split always comes back from the server after POST /v1/payments.
async function loadOpenInvoices() {
  openStatus.value = 'loading'
  try {
    const response = await api.request<{ data: StatementRow[] }>(`/v1/units/${props.unitId}/statement?page_size=50`)
    const rows: { label: string; due: number; paid: number }[] = []
    for (const row of response.data) {
      if (Number(row.rent_due) > Number(row.rent_paid)) {
        rows.push({ label: `Rent — ${monthLabel(row.period_month)}`, due: Number(row.rent_due), paid: Number(row.rent_paid) })
      }
      if (Number(row.water_due) > Number(row.water_paid)) {
        rows.push({ label: `Water & garbage — ${monthLabel(row.period_month)}`, due: Number(row.water_due), paid: Number(row.water_paid) })
      }
    }
    openRows.value = rows
    openStatus.value = rows.length ? 'ready' : 'empty'
  } catch {
    openStatus.value = 'error'
  }
}

function monthLabel(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', year: 'numeric' })
}

function purposeLabel(invoiceType: string, periodMonth: string) {
  const label = invoiceType === 'water_garbage' ? 'Water & garbage' : 'Rent'
  return `${label} — ${monthLabel(periodMonth)}`
}

async function submit() {
  if (!amount.value || amount.value <= 0 || !referenceNumber.value.trim()) return
  submitError.value = ''
  submitting.value = true
  try {
    const response = await api.request<{ data: PaymentResult }>('/v1/payments', {
      method: 'POST',
      body: {
        unit_id: props.unitId,
        amount: Math.round(amount.value * 100), // KES -> cents
        payment_method: method.value,
        reference_number: referenceNumber.value.trim(),
        notes: notes.value,
      },
    })
    result.value = response.data
    emit('success')
  } catch (e: any) {
    submitError.value = e?.data?.error ?? 'Unable to record this payment.'
  } finally {
    submitting.value = false
  }
}

// Reverse (spec 5.2) — offered right on the leg we just posted, since there
// is no endpoint to browse past transfers by unit; a reason is required and
// sent as-is, matching the backend's now-required reason field.
const reverseReason = ref<Record<string, string>>({})
const reversing = ref<Record<string, boolean>>({})
const reverseError = ref<Record<string, string>>({})
const reversed = ref<Record<string, boolean>>({})

async function reverseLeg(leg: AllocationLeg) {
  const reason = reverseReason.value[leg.transfer_id]?.trim()
  if (!reason) return
  reverseError.value[leg.transfer_id] = ''
  reversing.value[leg.transfer_id] = true
  try {
    await api.request(`/v1/payments/${leg.transfer_id}/reverse`, {
      method: 'POST',
      body: { reason, idempotency_key: `reverse-${leg.transfer_id}-${Date.now()}` },
    })
    reversed.value[leg.transfer_id] = true
  } catch (e: any) {
    reverseError.value[leg.transfer_id] = e?.data?.error ?? 'Unable to reverse this payment.'
  } finally {
    reversing.value[leg.transfer_id] = false
  }
}

function reset() {
  amount.value = null
  method.value = 'cash'
  referenceNumber.value = ''
  notes.value = ''
  result.value = null
  submitError.value = ''
}

function close() {
  reset()
  emit('close')
}

watch(() => props.open, (isOpen) => {
  if (isOpen) {
    reset()
    loadOpenInvoices()
  }
})
</script>

<template>
  <div v-if="open" class="drawer-backdrop" @click.self="close">
    <aside class="drawer">
      <header class="drawer__header">
        <h2 class="drawer__title">Record payment</h2>
        <button type="button" class="drawer__close" aria-label="Close" @click="close">&times;</button>
      </header>

      <template v-if="!result">
        <section class="open-invoices">
          <h3 class="section-title">Open invoices</h3>
          <p v-if="openStatus === 'loading'" class="hint">Loading&hellip;</p>
          <p v-else-if="openStatus === 'error'" class="hint hint--error">Could not load open invoices.</p>
          <p v-else-if="openStatus === 'empty'" class="hint">No open invoices for this unit.</p>
          <ul v-else class="open-invoices__list">
            <li v-for="row in openRows" :key="row.label">
              <span>{{ row.label }}</span>
              <span>KES {{ (row.due - row.paid).toLocaleString() }} owing</span>
            </li>
          </ul>
        </section>

        <form class="payment-form" @submit.prevent="submit">
          <label class="field">
            <span>Amount (KES)</span>
            <input v-model.number="amount" type="number" min="1" step="0.01" required>
          </label>
          <label class="field">
            <span>Method</span>
            <select v-model="method">
              <option value="cash">Cash</option>
              <option value="mpesa">M-Pesa</option>
              <option value="bank">Bank</option>
              <option value="card">Card</option>
            </select>
          </label>
          <label class="field">
            <span>Reference number</span>
            <input v-model="referenceNumber" type="text" required>
          </label>
          <label class="field">
            <span>Notes (optional)</span>
            <input v-model="notes" type="text">
          </label>
          <p v-if="submitError" class="hint hint--error">{{ submitError }}</p>
          <button type="submit" class="primary-button" :disabled="submitting">
            {{ submitting ? 'Recording...' : 'Record payment' }}
          </button>
        </form>
      </template>

      <!-- The real allocation breakdown, exactly as the server computed it —
           never a client-guessed split. -->
      <section v-else class="result">
        <h3 class="section-title">Applied</h3>
        <ul class="result__list">
          <li v-for="leg in result.allocations" :key="leg.invoice_id" class="result__leg">
            <div class="result__leg-row">
              <span>{{ purposeLabel(leg.invoice_type, leg.period_month) }}</span>
              <span>KES {{ (leg.applied / 100).toLocaleString() }}</span>
            </div>

            <p v-if="reversed[leg.transfer_id]" class="hint">Reversed.</p>
            <RoleGate v-else action="void_payment">
              <details class="reverse">
                <summary>Reverse</summary>
                <div class="reverse__form">
                  <input v-model="reverseReason[leg.transfer_id]" type="text" placeholder="Reason (required)">
                  <button
                    type="button"
                    class="secondary-button"
                    :disabled="reversing[leg.transfer_id] || !reverseReason[leg.transfer_id]?.trim()"
                    @click="reverseLeg(leg)"
                  >
                    {{ reversing[leg.transfer_id] ? 'Reversing...' : 'Confirm reverse' }}
                  </button>
                  <p v-if="reverseError[leg.transfer_id]" class="hint hint--error">{{ reverseError[leg.transfer_id] }}</p>
                </div>
              </details>
            </RoleGate>
          </li>
        </ul>
        <p v-if="result.unallocated_amount > 0" class="result__credit">
          KES {{ (result.unallocated_amount / 100).toLocaleString() }} left over — no open invoices remain to apply it to. Recorded as a credit, not dropped.
        </p>
        <button type="button" class="primary-button" @click="close">Done</button>
      </section>
    </aside>
  </div>
</template>

<style scoped>
.drawer-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(31, 35, 32, 0.4);
  display: flex;
  justify-content: flex-end;
  z-index: 50;
}

.drawer {
  width: min(420px, 100%);
  height: 100%;
  overflow-y: auto;
  padding: var(--space-6);
  background: var(--surface-card, #FFFFFF);
  box-shadow: var(--shadow-card);
  display: grid;
  gap: var(--space-6);
  align-content: start;
}

.drawer__header { display: flex; align-items: center; justify-content: space-between; }
.drawer__title { margin: 0; font-family: var(--font-display, serif); color: var(--color-text-primary, #1F2320); }
.drawer__close { border: 0; background: transparent; font-size: 1.5rem; line-height: 1; cursor: pointer; color: var(--color-text-muted, #6B7280); }

.section-title { margin: 0 0 var(--space-2); font-size: var(--text-ui-base); color: var(--color-text-primary, #1F2320); }

.open-invoices__list, .result__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--space-2);
}
.open-invoices__list li, .result__list li {
  display: flex;
  justify-content: space-between;
  padding: var(--space-2) var(--space-3);
  background: var(--surface-panel, #F5F6F8);
  border-radius: var(--radius-sm, 4px);
  font-size: var(--text-ui-sm);
}

.hint { color: var(--color-text-muted, #6B7280); font-size: var(--text-ui-sm); }
.hint--error { color: var(--color-status-error, #C6362E); }

.payment-form { display: grid; gap: var(--space-3); }
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

.result__credit {
  padding: var(--space-3);
  border-radius: var(--radius-sm, 4px);
  background: var(--color-status-warning-bg, rgba(201,130,26,0.12));
  color: var(--color-status-warning, #C9821A);
  font-size: var(--text-ui-sm);
}

.result__leg { display: grid; gap: var(--space-1); }
.result__leg-row { display: flex; justify-content: space-between; }

.reverse summary {
  cursor: pointer;
  font-size: var(--text-ui-xs);
  color: var(--color-status-error, #C6362E);
}
.reverse__form { display: flex; gap: var(--space-2); margin-top: var(--space-2); flex-wrap: wrap; }
.reverse__form input {
  flex: 1;
  min-width: 140px;
  padding: var(--space-1) var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.secondary-button {
  padding: var(--space-1) var(--space-3);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  background: var(--surface-card, #FFFFFF);
  color: var(--color-text-primary, #1F2320);
  cursor: pointer;
}
.secondary-button:disabled { opacity: 0.6; cursor: default; }

@media (max-width: 480px) {
  .drawer { width: 100%; }
}
</style>
