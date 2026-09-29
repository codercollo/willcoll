import { defineStore } from 'pinia'
import type { PageMeta } from '~/types/api'
import type { AllocationLine, ReviewPayment } from '~/types/payments'

export const usePayments = defineStore('payments', () => {
  const api = useApi()
  const queue = ref<ReviewPayment[]>([])
  const meta = ref<Partial<PageMeta> | null>(null)
  /** Total waiting, for the sidebar badge. */
  const waiting = ref(0)

  async function fetchQueue(page = 1) {
    const res = await api.get<{ payments: ReviewPayment[]; metadata: Partial<PageMeta> }>('/payments/review', { page, page_size: 20 })
    queue.value = res.payments ?? []
    meta.value = res.metadata
    waiting.value = res.metadata?.total_records ?? queue.value.length
  }

  /** Just the count (cheap page of one), for the badge. */
  async function refreshCount() {
    const res = await api.get<{ payments: ReviewPayment[]; metadata: Partial<PageMeta> }>('/payments/review', { page: 1, page_size: 1 })
    waiting.value = res.metadata?.total_records ?? 0
  }

  /** Place an unallocated payment on one unit. Amounts must add up to exactly the payment. */
  async function allocate(paymentId: string, unitId: string, allocations: AllocationLine[]) {
    await api.post(`/payments/${paymentId}/allocate`, { unit_id: unitId, allocations })
  }

  /** Accept the engine's placement (clears "placed on a guess"). */
  async function confirm(paymentId: string) {
    await api.post(`/payments/${paymentId}/confirm`)
  }

  return { queue, meta, waiting, fetchQueue, refreshCount, allocate, confirm }
})
