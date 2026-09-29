<script setup lang="ts">
import type { PageMeta } from '~/types/api'
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
        <table class="w-full text-left">
          <thead>
            <tr class="h-10 border-b border-border-default bg-gray-50">
              <th class="th-text px-5 font-medium">Firm</th><th class="th-text px-5 font-medium">Contact</th><th class="th-text px-5 font-medium">Status</th>
              <th class="th-text px-5 font-medium">Subscription</th><th class="th-text px-5 text-right font-medium">Properties</th><th class="th-text px-5 text-right font-medium">Units</th><th class="th-text px-5 font-medium">Joined</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in rows" :key="m.id" class="h-14 border-b border-border-subtle hover:bg-blue-50">
              <td class="px-5"><NuxtLink :to="`/admin/managers/${m.id}`" class="font-semibold text-accent-text">{{ m.firm_name }}</NuxtLink><p class="text-[length:var(--text-caption)] text-gray-500">{{ m.username }}</p></td>
              <td class="px-5"><p>{{ m.email }}</p><p class="tnum text-[length:var(--text-caption)] text-gray-500">{{ m.phone }}</p></td>
              <td class="px-5"><AdminStatusBadge kind="manager" :status="m.status" /></td>
              <td class="px-5"><AdminStatusBadge kind="subscription" :status="m.subscription?.status" /><p v-if="m.subscription?.current_period_end" class="text-[length:var(--text-caption)] text-gray-500">until {{ formatDate(m.subscription.current_period_end) }}</p></td>
              <td class="tnum px-5 text-right">{{ m.property_count ?? 0 }}</td>
              <td class="tnum px-5 text-right">{{ m.unit_count ?? 0 }}</td>
              <td class="tnum px-5 whitespace-nowrap">{{ formatDate(m.created_at) }}</td>
            </tr>
          </tbody>
        </table>
        <UiPagination :meta="meta" @change="(n) => (page = n)" />
      </div>
    </UiCard>
  </div>
</template>
