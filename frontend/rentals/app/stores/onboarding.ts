import { defineStore } from 'pinia'
import type { OnboardingStatus, OnboardSummary } from '~/types/onboarding'

export const useOnboarding = defineStore('onboarding', () => {
  const api = useApi()
  const status = ref<OnboardingStatus | null>(null)

  async function fetchStatus() {
    status.value = (await api.get<{ onboarding: OnboardingStatus }>('/onboarding')).onboarding
    return status.value
  }

  /** How many required steps are left, for the sidebar badge. */
  const remaining = computed(() => (status.value && !status.value.complete ? status.value.total - status.value.done : 0))

  /**
   * Upload the sheet. With dryRun the API checks it and reports what WOULD
   * happen; nothing is written until it is sent again without dryRun. A rejected
   * file throws an ApiError whose details.rows lists every problem by file row.
   */
  async function importSheet(propertyId: string, file: File, opts: { dryRun: boolean; asAt: string }) {
    const form = new FormData()
    form.append('file', file)
    const res = await api.request<{ summary?: OnboardSummary; imported?: OnboardSummary; as_at: string }>(`/properties/${propertyId}/onboarding/import`, {
      method: 'POST', rawBody: form, query: { dry_run: opts.dryRun ? 'true' : undefined, as_at: opts.asAt },
    })
    return { summary: (res.summary ?? res.imported)!, asAt: res.as_at }
  }

  return { status, remaining, fetchStatus, importSheet }
})
