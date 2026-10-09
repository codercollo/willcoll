<script setup lang="ts">
import type { ReviewPayment } from '~/types/payments'

definePageMeta({ title: 'Payments to review' })

const store = usePayments()
const toast = useToast()
const cur = useCurrency()

const loading = ref(true)
const error = ref('')
const page = ref(1)
const filter = ref<'all' | 'unmatched' | 'guess' | 'pending'>('all')

async function load() {
  loading.value = true
  error.value = ''
  try { await store.fetchQueue(page.value) } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load payments.' } finally { loading.value = false }
}
onMounted(load)
watch(page, load)

const isPending = (p: ReviewPayment) => p.status === 'pending'
const isGuess = (p: ReviewPayment) => !isPending(p) && p.auto_applied_unconfirmed
const kind = (p: ReviewPayment): 'pending' | 'guess' | 'unmatched' => (isPending(p) ? 'pending' : isGuess(p) ? 'guess' : 'unmatched')
const rows = computed(() => store.queue.filter(p => filter.value === 'all' || kind(p) === filter.value))
const counts = computed(() => ({
  all: store.queue.length,
  pending: store.queue.filter(isPending).length,
  guess: store.queue.filter(isGuess).length,
  unmatched: store.queue.filter(p => kind(p) === 'unmatched').length,
}))
const tabs = computed(() => [
  { key: 'all', label: `All (${counts.value.all})` },
  { key: 'pending', label: `Cash to confirm (${counts.value.pending})` },
  { key: 'unmatched', label: `Unmatched (${counts.value.unmatched})` },
  { key: 'guess', label: `Placed on a guess (${counts.value.guess})` },
])

// ---- reject a pending cash payment ---------------------------------------
const rejecting = ref<ReviewPayment | null>(null)
const rejectOpen = computed({ get: () => !!rejecting.value, set: (v) => { if (!v) { rejecting.value = null; rejectReason.value = '' } } })
const rejectReason = ref('')
const rejectBusy = ref(false)
async function reject() {
  const p = rejecting.value
  if (!p) return
  if (!rejectReason.value.trim()) { toast.fail(new Error('Say why you are rejecting it.')); return }
  rejectBusy.value = true
  try {
    await store.reject(p.id, rejectReason.value.trim())
    toast.success(`Rejected: Ksh ${cur.format(p.amount)} for ${p.unit_code}. Nothing was posted and the owner has been told.`)
    rejectOpen.value = false
  } catch (e) { toast.fail(e) } finally { rejectBusy.value = false; await load() }
}

// ---- confirm -------------------------------------------------------------
const confirming = ref('')
async function confirm(p: ReviewPayment) {
  confirming.value = p.id
  try {
    await store.confirm(p.id)
    toast.success(isPending(p) ? `Confirmed and posted: Ksh ${cur.format(p.amount)} for ${p.unit_code}.` : `Confirmed: Ksh ${cur.format(p.amount)} for ${p.unit_code}.`)
  } catch (e) {
    // 409: someone already confirmed it. 403: you recorded it yourself, so someone else must confirm it.
    // Either way the queue is stale.
    toast.fail(e)
  } finally { confirming.value = ''; await load() }
}

// ---- assign --------------------------------------------------------------
const assigning = ref<ReviewPayment | null>(null)
const assignOpen = computed({ get: () => !!assigning.value, set: (v) => { if (!v) assigning.value = null } })
</script>

<template>
  <div class="flex flex-col gap-6">
    <UiFormAlert v-if="store.pendingOver48h" data-testid="stale-pending">
      {{ store.pendingOver48h }} cash {{ store.pendingOver48h === 1 ? 'payment has' : 'payments have' }} been waiting for a decision for more than 48 hours. Nothing is credited to the tenant until it is confirmed.
    </UiFormAlert>
    <div class="grid gap-4 md:grid-cols-3">
      <div class="card flex gap-3 p-5">
        <UiBadge status="pending" label="Cash to confirm" class="shrink-0 self-start" />
        <p class="text-gray-700">Cash someone entered by hand. <strong>Nothing is credited</strong> until you confirm it, and the person who entered it cannot. Reject it, with a reason, if it is wrong.</p>
      </div>
      <div class="card flex gap-3 p-5">
        <UiBadge status="review" label="Unmatched" class="shrink-0 self-start" />
        <p class="text-gray-700">We could not tell which unit this belongs to, or how to split it. <strong>Assign it to a unit</strong> and choose where the money goes.</p>
      </div>
      <div class="card flex gap-3 p-5">
        <UiBadge status="partial" label="Placed on a guess" class="shrink-0 self-start" />
        <p class="text-gray-700">Applied automatically by a default rule. <strong>Confirm</strong> it if it is right; if not, open the unit ledger and reverse the entries.</p>
      </div>
    </div>

    <UiTabs v-model="filter" :tabs="tabs" />

    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading && !store.queue.length" block />

    <UiCard v-else-if="!error" :padded="false">
      <UiEmptyState v-if="!rows.length" :title="store.queue.length ? 'Nothing in this view' : 'All caught up'" :text="store.queue.length ? 'Try another filter.' : 'Every payment has been placed. New ones that need a decision show up here.'" icon="lucide:circle-check" />
      <div v-else class="overflow-x-auto">
        <table class="stack-table w-full text-left">
          <thead>
            <tr class="h-10 border-b border-border-default bg-gray-50">
              <th class="th-text px-5 font-medium">Received</th><th class="th-text px-5 font-medium">Payer</th>
              <th class="th-text px-5 font-medium">Unit</th><th class="th-text px-5 text-right font-medium">Amount</th>
              <th class="th-text px-5 font-medium">Why it is here</th><th class="th-text px-5 font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in rows" :key="p.id" class="border-b border-border-subtle align-top hover:bg-blue-50">
              <td class="px-5 py-4 whitespace-nowrap" data-label="Received"><p class="tnum">{{ formatDateTime(p.received_at) }}</p><p class="tnum text-[length:var(--text-caption)] text-gray-500">{{ p.mpesa_receipt }}</p></td>
              <td class="px-5 py-4" data-label="Payer"><p class="font-semibold">{{ p.payer_name || 'Unknown' }}</p><p class="tnum text-[length:var(--text-caption)] text-gray-500">{{ p.msisdn }}</p></td>
              <td class="px-5 py-4" data-label="Unit">
                <NuxtLink v-if="p.matched_unit_id" :to="`/units/${p.matched_unit_id}`" class="tnum font-semibold text-accent-text">{{ p.unit_code }}</NuxtLink>
                <span v-else class="text-gray-500">—</span>
              </td>
              <td class="money px-5 py-4 text-right font-semibold whitespace-nowrap" data-label="Amount">{{ cur.format(p.amount) }}</td>
              <td class="max-w-xs px-5 py-4" data-label="Why it is here" data-wide>
                <UiBadge v-if="isPending(p)" status="pending" label="Cash to confirm" />
                <UiBadge v-else-if="isGuess(p)" status="partial" label="Placed on a guess" />
                <UiBadge v-else status="review" label="Unmatched" />
                <p v-if="isPending(p)" class="mt-2 text-gray-700">Entered by {{ p.recorded_by_role ?? 'staff' }}{{ p.recorded_at ? ` on ${formatDateTime(p.recorded_at)}` : '' }}; posts to {{ (p.pending_ledger_type ?? 'rent').toLowerCase() }} once confirmed.</p>
                <p v-if="p.review_note" class="mt-2 text-gray-700">{{ p.review_note }}</p>
              </td>
              <td class="px-5 py-4">
                <div class="flex flex-wrap gap-2">
                  <template v-if="isPending(p)">
                    <UiButton :loading="confirming === p.id" @click="confirm(p)"><Icon name="lucide:check" class="size-4" />Confirm</UiButton>
                    <UiButton variant="secondary" @click="rejecting = p">Reject</UiButton>
                  </template>
                  <template v-else-if="isGuess(p)">
                    <UiButton :loading="confirming === p.id" @click="confirm(p)"><Icon name="lucide:check" class="size-4" />Confirm</UiButton>
                    <UiButton v-if="p.matched_unit_id" variant="secondary" :to="`/units/${p.matched_unit_id}`">Open ledger</UiButton>
                  </template>
                  <UiButton v-else @click="assigning = p">Assign to a unit</UiButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        <UiPagination :meta="store.meta" @change="(n) => (page = n)" />
      </div>
    </UiCard>

    <PaymentAllocateModal v-model="assignOpen" :payment="assigning" @placed="load" />

    <UiModal v-model="rejectOpen" title="Reject this cash payment" width="sm">
      <p class="mb-4 text-gray-700">Nothing is posted and the owner is told. Say why, so the record shows it.</p>
      <UiField label="Reason" for="reject-reason">
        <UiInput id="reject-reason" v-model="rejectReason" placeholder="e.g. no cash was handed over" />
      </UiField>
      <template #footer>
        <UiButton variant="secondary" @click="rejectOpen = false">Cancel</UiButton>
        <UiButton variant="destructive" :loading="rejectBusy" @click="reject">Reject payment</UiButton>
      </template>
    </UiModal>
  </div>
</template>
