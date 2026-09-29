<script setup lang="ts">
import type { ReviewPayment } from '~/types/payments'

definePageMeta({ title: 'Payments to review' })

const store = usePayments()
const toast = useToast()
const cur = useCurrency()

const loading = ref(true)
const error = ref('')
const page = ref(1)
const filter = ref<'all' | 'unmatched' | 'guess'>('all')

async function load() {
  loading.value = true
  error.value = ''
  try { await store.fetchQueue(page.value) } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load payments.' } finally { loading.value = false }
}
onMounted(load)
watch(page, load)

const isGuess = (p: ReviewPayment) => p.auto_applied_unconfirmed
const rows = computed(() => store.queue.filter(p => filter.value === 'all' || (filter.value === 'guess' ? isGuess(p) : !isGuess(p))))
const counts = computed(() => ({ all: store.queue.length, guess: store.queue.filter(isGuess).length, unmatched: store.queue.filter(p => !isGuess(p)).length }))
const tabs = computed(() => [
  { key: 'all', label: `All (${counts.value.all})` },
  { key: 'unmatched', label: `Unmatched (${counts.value.unmatched})` },
  { key: 'guess', label: `Placed on a guess (${counts.value.guess})` },
])

// ---- confirm -------------------------------------------------------------
const confirming = ref('')
async function confirm(p: ReviewPayment) {
  confirming.value = p.id
  try {
    await store.confirm(p.id)
    toast.success(`Confirmed: Ksh ${cur.format(p.amount)} for ${p.unit_code}.`)
  } catch (e) {
    // 409: someone already confirmed it. Either way the queue is stale.
    toast.fail(e)
  } finally { confirming.value = ''; await load() }
}

// ---- assign --------------------------------------------------------------
const assigning = ref<ReviewPayment | null>(null)
const assignOpen = computed({ get: () => !!assigning.value, set: (v) => { if (!v) assigning.value = null } })
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="grid gap-4 md:grid-cols-2">
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
        <table class="w-full text-left">
          <thead>
            <tr class="h-10 border-b border-border-default bg-gray-50">
              <th class="th-text px-5 font-medium">Received</th><th class="th-text px-5 font-medium">Payer</th>
              <th class="th-text px-5 font-medium">Unit</th><th class="th-text px-5 text-right font-medium">Amount</th>
              <th class="th-text px-5 font-medium">Why it is here</th><th class="th-text px-5 font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in rows" :key="p.id" class="border-b border-border-subtle align-top hover:bg-blue-50">
              <td class="px-5 py-4 whitespace-nowrap"><p class="tnum">{{ formatDateTime(p.received_at) }}</p><p class="tnum text-[length:var(--text-caption)] text-gray-500">{{ p.mpesa_receipt }}</p></td>
              <td class="px-5 py-4"><p class="font-semibold">{{ p.payer_name || 'Unknown' }}</p><p class="tnum text-[length:var(--text-caption)] text-gray-500">{{ p.msisdn }}</p></td>
              <td class="px-5 py-4">
                <NuxtLink v-if="p.matched_unit_id" :to="`/units/${p.matched_unit_id}`" class="tnum font-semibold text-accent-text">{{ p.unit_code }}</NuxtLink>
                <span v-else class="text-gray-500">—</span>
              </td>
              <td class="money px-5 py-4 text-right font-semibold whitespace-nowrap">{{ cur.format(p.amount) }}</td>
              <td class="max-w-xs px-5 py-4">
                <UiBadge v-if="isGuess(p)" status="partial" label="Placed on a guess" />
                <UiBadge v-else status="review" label="Unmatched" />
                <p v-if="p.review_note" class="mt-2 text-gray-700">{{ p.review_note }}</p>
              </td>
              <td class="px-5 py-4">
                <div class="flex flex-wrap gap-2">
                  <template v-if="isGuess(p)">
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
  </div>
</template>
