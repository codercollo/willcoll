<script setup lang="ts">
import type { AdminPlan } from '~/types/admin'

definePageMeta({ title: 'Plans', layout: 'admin' })

const admin = useAdmin()
const cur = useCurrency()

const plans = ref<AdminPlan[]>([])
const plansError = ref('')
const editing = ref<AdminPlan | null>(null)
const planOpen = ref(false)
async function loadPlans() {
  plansError.value = ''
  try { plans.value = await admin.plans() } catch (e) { plansError.value = e instanceof Error ? e.message : 'Could not load plans.' }
}
function openPlan(p: AdminPlan | null) { editing.value = p; planOpen.value = true }
onMounted(loadPlans)

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
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <p class="text-gray-500">Until a plan exists, no manager can subscribe. Plans are never deleted.</p>
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

    <AdminPlanFormModal v-model="planOpen" :plan="editing" @saved="loadPlans" />
  </div>
</template>
