<script setup lang="ts">
import type { PageMeta } from '#base/components/ui/Pagination.vue'
import type { AdminSubscription } from '~/types/admin'

definePageMeta({ title: 'Subscriptions', layout: 'admin' })

const admin = useAdmin()
const cur = useCurrency()

const status = ref('')
const page = ref(1)
const rows = ref<AdminSubscription[]>([])
const meta = ref<Partial<PageMeta> | null>(null)
const loading = ref(true)
const error = ref('')
async function load() {
  loading.value = true
  error.value = ''
  try {
    const r = await admin.subscriptions({ status: status.value || undefined, page: page.value })
    rows.value = r.rows
    meta.value = r.meta
  } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load subscriptions.' } finally { loading.value = false }
}
onMounted(load)
watch(status, () => { page.value = 1; load() })
watch(page, load)

const tabs = [{ key: '', label: 'All' }, { key: 'active', label: 'Active' }, { key: 'trialing', label: 'Trial' }, { key: 'past_due', label: 'Past due' }, { key: 'cancelled', label: 'Cancelled' }]
</script>

<template>
  <div class="flex flex-col gap-4">
    <UiTabs v-model="status" :tabs="tabs" />
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading && !rows.length" block />
    <UiCard v-else-if="!error" :padded="false">
      <UiEmptyState v-if="!rows.length" title="No subscriptions" text="Nothing matches this filter." icon="lucide:receipt" />
      <div v-else class="overflow-x-auto">
        <table class="w-full text-left">
          <thead><tr class="h-10 border-b border-border-default bg-gray-50"><th class="th-text px-5 font-medium">Firm</th><th class="th-text px-5 font-medium">Product</th><th class="th-text px-5 font-medium">Plan</th><th class="th-text px-5 font-medium">Status</th><th class="th-text px-5 font-medium">Period</th></tr></thead>
          <tbody>
            <tr v-for="s in rows" :key="s.id" class="h-14 border-b border-border-subtle hover:bg-blue-50">
              <td class="px-5"><NuxtLink :to="`/admin/managers/${s.manager_id}`" class="font-semibold text-accent-text">{{ s.firm_name }}</NuxtLink></td>
              <td class="px-5 capitalize">{{ s.product ?? 'rentals' }}</td>
              <td class="px-5">{{ s.plan_name }} <span class="money text-gray-500">Ksh {{ cur.format(s.plan_price) }} / {{ s.billing_interval }}</span></td>
              <td class="px-5"><AdminStatusBadge kind="subscription" :status="s.status" /></td>
              <td class="tnum px-5 whitespace-nowrap">{{ formatDate(s.current_period_start) }} – {{ formatDate(s.current_period_end) }}</td>
            </tr>
          </tbody>
        </table>
        <UiPagination :meta="meta" @change="(n) => (page = n)" />
      </div>
    </UiCard>
  </div>
</template>
