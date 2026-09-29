<script setup lang="ts">
import type { PageMeta } from '~/types/api'
import type { AdminPlan, AdminSubscription } from '~/types/admin'

definePageMeta({ title: 'Subscriptions', layout: 'admin' })

const admin = useAdmin()
const cur = useCurrency()

// ---- plans ---------------------------------------------------------------
const plans = ref<AdminPlan[]>([])
const plansError = ref('')
const editing = ref<AdminPlan | null>(null)
const planOpen = ref(false)
async function loadPlans() {
  plansError.value = ''
  try { plans.value = await admin.plans() } catch (e) { plansError.value = e instanceof Error ? e.message : 'Could not load plans.' }
}
function openPlan(p: AdminPlan | null) { editing.value = p; planOpen.value = true }

const planTab = ref<'active' | 'archived'>('active')
const planTabs = [{ key: 'active', label: 'Active' }, { key: 'archived', label: 'Archived' }]
const visiblePlans = computed(() => plans.value.filter(p => (planTab.value === 'archived') === p.archived))

function planPrice(p: AdminPlan) {
  return p.pricing_type === 'per_unit'
    ? `Ksh ${cur.format(p.per_unit_price ?? '0')}/unit · min ${cur.format(p.min_price ?? '0')}`
    : `Ksh ${cur.format(p.price)}`
}
function planStatus(p: AdminPlan): { status: 'paid' | 'vacant' | 'pending'; label: string } {
  if (p.is_test) return { status: 'pending', label: 'TEST' }
  if (p.archived) return { status: 'vacant', label: 'Archived' }
  return { status: 'paid', label: 'Active' }
}

const archiveTarget = ref<AdminPlan | null>(null)
const archiving = ref(false)
async function confirmArchive() {
  if (!archiveTarget.value) return
  archiving.value = true
  try {
    const target = archiveTarget.value
    const updated = target.archived ? await admin.restorePlan(target.id) : await admin.archivePlan(target.id)
    plans.value = plans.value.map(p => (p.id === updated.id ? updated : p))
    archiveTarget.value = null
  } catch (e) { plansError.value = e instanceof Error ? e.message : 'Could not update the plan.' } finally { archiving.value = false }
}

// ---- subscriptions -------------------------------------------------------
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
onMounted(() => { loadPlans(); load() })
watch(status, () => { page.value = 1; load() })
watch(page, load)

const tabs = [{ key: '', label: 'All' }, { key: 'active', label: 'Active' }, { key: 'trialing', label: 'Trial' }, { key: 'past_due', label: 'Past due' }, { key: 'cancelled', label: 'Cancelled' }]
</script>

<template>
  <div class="flex flex-col gap-8">
    <section class="flex flex-col gap-4">
      <div class="flex items-center justify-between gap-4">
        <div><h2>Plans</h2><p class="text-gray-500">Until a plan exists, no manager can subscribe. Plans are never deleted.</p></div>
        <UiButton @click="openPlan(null)"><Icon name="lucide:plus" class="size-4" />New plan</UiButton>
      </div>
      <UiFormAlert v-if="plansError">{{ plansError }} <button type="button" class="font-semibold text-accent-text" @click="loadPlans">Retry</button></UiFormAlert>
      <UiTabs v-model="planTab" :tabs="planTabs" />
      <UiCard :padded="false">
        <UiEmptyState v-if="!visiblePlans.length && !plansError" :title="planTab === 'archived' ? 'No archived plans' : 'No plans yet'" text="Create the first plan so managers can subscribe." icon="lucide:receipt" />
        <div v-else-if="visiblePlans.length" class="overflow-x-auto">
          <table class="w-full text-left">
            <thead><tr class="h-10 border-b border-border-default bg-gray-50"><th class="th-text px-5 font-medium">Plan</th><th class="th-text px-5 font-medium">Type</th><th class="th-text px-5 font-medium">Price</th><th class="th-text px-5 font-medium">Billed</th><th class="th-text px-5 text-right font-medium">Unit limit</th><th class="th-text px-5 text-right font-medium">Subscribers</th><th class="th-text px-5 font-medium">Status</th><th class="w-12" /></tr></thead>
            <tbody>
              <tr v-for="p in visiblePlans" :key="p.id" class="h-12 border-b border-border-subtle hover:bg-blue-50">
                <td class="px-5 font-semibold">{{ p.name }}</td>
                <td class="px-5">{{ p.pricing_type === 'per_unit' ? 'Per unit' : 'Flat' }}</td>
                <td class="money px-5">{{ planPrice(p) }}</td>
                <td class="px-5 capitalize">{{ p.billing_interval }}</td>
                <td class="tnum px-5 text-right">{{ p.unit_cap ?? 'No limit' }}</td>
                <td class="tnum px-5 text-right">{{ p.subscribers }}</td>
                <td class="px-5"><UiBadge v-bind="planStatus(p)" /></td>
                <td class="flex items-center gap-1 px-3">
                  <UiButton variant="icon" :label="`Edit ${p.name}`" @click="openPlan(p)"><Icon name="lucide:pencil" class="size-5" /></UiButton>
                  <UiButton variant="icon" :label="p.archived ? `Restore ${p.name}` : `Archive ${p.name}`" @click="archiveTarget = p">
                    <Icon :name="p.archived ? 'lucide:rotate-ccw' : 'lucide:archive'" class="size-5" />
                  </UiButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </UiCard>
    </section>

    <UiModal :model-value="!!archiveTarget" :title="archiveTarget?.archived ? 'Restore plan' : 'Archive plan'" width="sm" @update:model-value="(v) => { if (!v) archiveTarget = null }">
      <p v-if="archiveTarget && !archiveTarget.archived" class="text-gray-700">
        Managers will no longer see <strong>{{ archiveTarget.name }}</strong> or be able to renew onto it.
        Existing subscribers keep their current period and switch plans when it ends. History is unaffected.
      </p>
      <p v-else-if="archiveTarget" class="text-gray-700">
        <strong>{{ archiveTarget.name }}</strong> will be visible to managers again.
      </p>
      <template #footer>
        <UiButton variant="secondary" @click="archiveTarget = null">Cancel</UiButton>
        <UiButton :loading="archiving" @click="confirmArchive">{{ archiveTarget?.archived ? 'Restore' : 'Archive' }}</UiButton>
      </template>
    </UiModal>

    <section class="flex flex-col gap-4">
      <h2>Subscriptions</h2>
      <UiTabs v-model="status" :tabs="tabs" />
      <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
      <UiSkeleton v-if="loading && !rows.length" block />
      <UiCard v-else-if="!error" :padded="false">
        <UiEmptyState v-if="!rows.length" title="No subscriptions" text="Nothing matches this filter." icon="lucide:receipt" />
        <div v-else class="overflow-x-auto">
          <table class="w-full text-left">
            <thead><tr class="h-10 border-b border-border-default bg-gray-50"><th class="th-text px-5 font-medium">Firm</th><th class="th-text px-5 font-medium">Plan</th><th class="th-text px-5 font-medium">Status</th><th class="th-text px-5 font-medium">Period</th></tr></thead>
            <tbody>
              <tr v-for="s in rows" :key="s.id" class="h-14 border-b border-border-subtle hover:bg-blue-50">
                <td class="px-5"><NuxtLink :to="`/admin/managers/${s.manager_id}`" class="font-semibold text-accent-text">{{ s.firm_name }}</NuxtLink></td>
                <td class="px-5">{{ s.plan_name }} <span class="money text-gray-500">Ksh {{ cur.format(s.plan_price) }} / {{ s.billing_interval }}</span></td>
                <td class="px-5"><AdminStatusBadge kind="subscription" :status="s.status" /></td>
                <td class="tnum px-5 whitespace-nowrap">{{ formatDate(s.current_period_start) }} – {{ formatDate(s.current_period_end) }}</td>
              </tr>
            </tbody>
          </table>
          <UiPagination :meta="meta" @change="(n) => (page = n)" />
        </div>
      </UiCard>
    </section>

    <AdminPlanFormModal v-model="planOpen" :plan="editing" @saved="loadPlans" />
  </div>
</template>
