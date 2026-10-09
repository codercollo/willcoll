import { defineStore } from 'pinia'
import type { PageMeta } from '~/types/api'
import type { AllocationLine, PendingSummary, ReviewPayment } from '~/types/payments'

interface ReviewResponse { payments: ReviewPayment[]; metadata: Partial<PageMeta>; summary?: PendingSummary }

export const usePayments = defineStore('payments', () => {
  const api = useApi()
  const queue = ref<ReviewPayment[]>([])
  const meta = ref<Partial<PageMeta> | null>(null)
  /** Total waiting, for the sidebar badge. */
  const waiting = ref(0)
  /** Cash payments waiting for a decision, and how many have waited over 48 hours (the dashboard notice). */
  const pending = ref(0)
  const pendingOver48h = ref(0)

  function take(res: ReviewResponse) {
    waiting.value = res.metadata?.total_records ?? 0
    pending.value = res.summary?.pending ?? 0
    pendingOver48h.value = res.summary?.pending_over_48h ?? 0
  }

  async function fetchQueue(page = 1) {
    const res = await api.get<ReviewResponse>('/payments/review', { page, page_size: 20 })
    queue.value = res.payments ?? []
    meta.value = res.metadata
    take(res)
    if (!res.metadata?.total_records) waiting.value = queue.value.length
  }

  /** Just the counts (cheap page of one), for the badge and the dashboard notice. */
  async function refreshCount() {
    take(await api.get<ReviewResponse>('/payments/review', { page: 1, page_size: 1 }))
  }

  /** Place an unallocated payment on one unit. Amounts must add up to exactly the payment. */
  async function allocate(paymentId: string, unitId: string, allocations: AllocationLine[]) {
    await api.post(`/payments/${paymentId}/allocate`, { unit_id: unitId, allocations })
  }

  /**
   * Accept the engine's placement (clears "placed on a guess"), or confirm a
   * pending cash payment, which posts it to the ledger. Whoever recorded it
   * cannot (403).
   */
  async function confirm(paymentId: string) {
    await api.post(`/payments/${paymentId}/confirm`)
  }

  /** Refuse a pending cash payment with a reason. Nothing posts; the owner is alerted. */
  async function reject(paymentId: string, reason: string) {
    await api.post(`/payments/${paymentId}/reject`, { reason })
  }

  return { queue, meta, waiting, pending, pendingOver48h, fetchQueue, refreshCount, allocate, confirm, reject }
})
