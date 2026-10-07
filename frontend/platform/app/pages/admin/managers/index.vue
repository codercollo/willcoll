<script setup lang="ts">
import type { PageMeta } from '#base/components/ui/Pagination.vue'
import type { AdminManager } from '~/types/admin'

definePageMeta({ title: 'Managers', layout: 'admin' })

const admin = useAdmin()
const status = ref('')
const page = ref(1)
const rows = ref<AdminManager[]>([])
const meta = ref<Partial<PageMeta> | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    const r = await admin.managers({ status: status.value || undefined, page: page.value, sort: '-created_at' })
    rows.value = r.rows
    meta.value = r.meta
  } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load managers.' } finally { loading.value = false }
}
onMounted(load)
watch(status, () => { page.value = 1; load() })
watch(page, load)

const tabs = [{ key: '', label: 'All' }, { key: 'active', label: 'Active' }, { key: 'pending', label: 'Pending' }, { key: 'suspended', label: 'Suspended' }]
</script>

<template>
  <div class="flex flex-col gap-6">
    <UiTabs v-model="status" :tabs="tabs" />
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading && !rows.length" block />
    <UiCard v-else-if="!error" :padded="false">
      <UiEmptyState v-if="!rows.length" title="No managers" text="Nothing matches this filter." icon="lucide:users" />
      <div v-else class="overflow-x-auto">
        <table class="stack-table w-full text-left">
          <thead>
            <tr class="h-10 border-b border-border-default bg-gray-50">
              <th class="th-text px-5 font-medium">Firm</th><th class="th-text px-5 font-medium">Contact</th><th class="th-text px-5 font-medium">Status</th>
              <th class="th-text px-5 font-medium">Subscription</th><th class="th-text px-5 text-right font-medium">Staff</th><th class="th-text px-5 text-right font-medium">Units</th><th class="th-text px-5 font-medium">Joined</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in rows" :key="m.id" class="h-14 border-b border-border-subtle hover:bg-blue-50">
              <td class="px-5" data-label="Firm" data-wide><NuxtLink :to="`/admin/managers/${m.id}`" class="font-semibold text-accent-text">{{ m.firm_name }}</NuxtLink><p class="text-[length:var(--text-caption)] text-gray-500">{{ m.username }}</p></td>
              <td class="px-5" data-label="Contact" data-wide><p>{{ m.email }}</p><p class="tnum text-[length:var(--text-caption)] text-gray-500">{{ m.phone }}</p></td>
              <td class="px-5" data-label="Status"><AdminStatusBadge kind="manager" :status="m.status" /></td>
              <td class="px-5" data-label="Subscription"><AdminStatusBadge kind="subscription" :status="m.subscription?.status" /><p v-if="m.subscription?.current_period_end" class="text-[length:var(--text-caption)] text-gray-500">until {{ formatDate(m.subscription.current_period_end) }}</p></td>
              <td class="tnum px-5 text-right" data-label="Staff">{{ m.staff_count ?? 0 }}</td>
              <td class="tnum px-5 text-right" data-label="Units">{{ m.unit_count ?? 0 }}</td>
              <td class="tnum px-5 whitespace-nowrap" data-label="Joined" data-wide>{{ formatDate(m.created_at) }}</td>
            </tr>
          </tbody>
        </table>
        <UiPagination :meta="meta" @change="(n) => (page = n)" />
      </div>
    </UiCard>
  </div>
</template>
