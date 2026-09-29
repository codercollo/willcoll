<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { Lease, Unit } from '~/types/api'

definePageMeta({ title: 'Unit' })

const route = useRoute()
const props_ = useProperties()
const units = useUnits()
const toast = useToast()
const propertyId = computed(() => String(route.params.id))
const unitId = computed(() => String(route.params.unitId))

const unit = ref<Unit | null>(null)
const leases = ref<Lease[]>([])
const loading = ref(true)
const notFound = ref(false)
const error = ref('')

const active = computed(() => leases.value.find(l => l.status === 'active') ?? null)
const history = computed(() => leases.value.filter(l => l.status !== 'active'))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [u, ls] = await Promise.all([units.get(unitId.value), units.leases(unitId.value)])
    unit.value = u
    leases.value = ls
    route.meta.title = `Unit ${u.unit_code}`
    if (props_.current?.id !== propertyId.value) await props_.fetchOne(propertyId.value).catch(() => {})
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else error.value = e instanceof Error ? e.message : 'Could not load the unit.'
  } finally { loading.value = false }
}
onMounted(load)

// ---- unit edit ------------------------------------------------------------
const editUnit = ref(false)
const uForm = reactive({ unit_code: '', meter_number: '' })
const uSub = useSubmit()
function openEditUnit() { uForm.unit_code = unit.value?.unit_code ?? ''; uForm.meter_number = unit.value?.meter_number ?? ''; uSub.fields.value = {}; uSub.message.value = ''; editUnit.value = true }
async function saveUnit() {
  const patch: { unit_code?: string; meter_number?: string } = {}
  if (uForm.unit_code.trim() !== unit.value?.unit_code) patch.unit_code = uForm.unit_code.trim()
  if (uForm.meter_number.trim() !== (unit.value?.meter_number ?? '')) patch.meter_number = uForm.meter_number.trim()
  const u = await uSub.run(() => units.update(unitId.value, patch))
  if (u) { unit.value = u; route.meta.title = `Unit ${u.unit_code}`; editUnit.value = false; toast.success('Unit saved') }
  else if (uSub.status.value === 409) { toast.error('Someone else changed this unit. Reloaded.'); editUnit.value = false; load() }
}

// ---- lease ----------------------------------------------------------------
const leaseModal = ref(false)
const leaseEditing = ref<Lease | null>(null)
function newLease() { leaseEditing.value = null; leaseModal.value = true }
function editLease() { leaseEditing.value = active.value; leaseModal.value = true }

const terminating = ref(false)
const term = useSubmit()
async function terminate() {
  if (!active.value) return
  const l = await term.run(() => units.updateLease(active.value!.id, { status: 'terminated' }))
  if (l) { terminating.value = false; toast.success('Lease terminated. The unit is now vacant.'); await load() }
}

// ---- payers ---------------------------------------------------------------
const payer = reactive({ name: '', phone: '' })
const pSub = useSubmit()
async function addPayer() {
  if (!active.value) return
  const p = await pSub.run(() => units.addPayer(active.value!.id, { name: payer.name.trim(), ...(payer.phone.trim() ? { phone: normalise(payer.phone) } : {}) }))
  if (p) { payer.name = ''; payer.phone = ''; toast.success('Co-payer added'); await load() }
}
async function removePayer(id: string) {
  if (!active.value) return
  try { await units.removePayer(active.value.id, id); toast.success('Co-payer removed'); await load() } catch (e) { toast.fail(e) }
}
function normalise(p: string) {
  const d = p.replace(/[\s-]/g, '')
  if (/^0[17]\d{8}$/.test(d)) return '+254' + d.slice(1)
  if (/^254\d{9}$/.test(d)) return '+' + d
  return d
}

const tab = ref('lease')
const tabs = [{ key: 'lease', label: 'Lease' }, { key: 'rent', label: 'Rent ledger' }, { key: 'water', label: 'Water ledger' }, { key: 'garbage', label: 'Garbage ledger' }, { key: 'electricity_deposit', label: 'Electricity deposit ledger' }]
const visibleTabs = computed(() => tabs.filter(t =>
  (t.key !== 'garbage' || props_.current?.garbage_enabled !== false)
  && (t.key !== 'electricity_deposit' || props_.current?.electricity_enabled === true)))
</script>

<template>
  <div class="flex flex-col gap-6">
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiCard v-if="notFound" :padded="false"><UiEmptyState title="Unit not found" icon="lucide:door-open"><UiButton :to="`/properties/${propertyId}`">Back to property</UiButton></UiEmptyState></UiCard>
    <UiSkeleton v-else-if="loading && !unit" block />

    <template v-else-if="unit">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <NuxtLink :to="`/properties/${propertyId}`" class="mb-2 inline-flex items-center gap-1 text-gray-500 hover:text-gray-900"><Icon name="lucide:chevron-left" class="size-4" />{{ props_.current?.name ?? 'Property' }}</NuxtLink>
          <p class="flex items-center gap-3 text-gray-500">
            <UiBadge :status="unit.status === 'vacant' ? 'vacant' : 'info'" :label="unit.status === 'vacant' ? 'Vacant' : 'Occupied'" />
            <span v-if="unit.meter_number">Meter {{ unit.meter_number }}</span>
          </p>
        </div>
        <UiButton variant="secondary" @click="openEditUnit"><Icon name="lucide:pencil" class="size-4" />Edit unit</UiButton>
      </header>

      <UiTabs v-model="tab" :tabs="visibleTabs" />

      <template v-if="tab === 'lease'">
        <!-- Current lease -->
        <UiCard v-if="active">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="text-[length:var(--text-caption)] text-gray-500">Current tenant</p>
              <h2>{{ active.tenant_name }}</h2>
              <p class="tnum text-gray-700">{{ active.primary_phone }}</p>
            </div>
            <div class="flex gap-3">
              <UiButton variant="secondary" @click="editLease">Edit lease</UiButton>
              <UiButton variant="destructive" @click="terminating = true">Terminate lease</UiButton>
            </div>
          </div>
          <dl class="mt-6 grid grid-cols-2 gap-6 md:grid-cols-4">
            <div><dt class="text-[length:var(--text-caption)] text-gray-500">Monthly rent</dt><dd class="money font-semibold">Ksh {{ formatMoney(active.rent_amount) }}</dd></div>
            <div><dt class="text-[length:var(--text-caption)] text-gray-500">Rent deposit</dt><dd class="money font-semibold">Ksh {{ formatMoney(active.rent_deposit_amount) }}</dd></div>
            <div><dt class="text-[length:var(--text-caption)] text-gray-500">Water deposit</dt><dd class="money font-semibold">Ksh {{ formatMoney(active.water_deposit_amount) }}</dd></div>
            <div><dt class="text-[length:var(--text-caption)] text-gray-500">Leased since</dt><dd class="font-semibold">{{ formatDate(active.start_date) }}</dd></div>
          </dl>
          <p class="mt-4 text-gray-500">A rent change applies to the next rent run, never to past months.</p>
        </UiCard>

        <UiCard v-else :padded="false">
          <UiEmptyState title="This unit is vacant" text="Start a lease to add a tenant and post their deposits." icon="lucide:door-open"><UiButton @click="newLease">Start a lease</UiButton></UiEmptyState>
        </UiCard>

        <!-- Door sticker for tenant payments -->
        <PropertyUnitQrCard v-if="active" :lease="active" :unit-code="unit.unit_code" :property-name="props_.current?.name" />

        <!-- Co-payers -->
        <UiCard v-if="active">
          <h2 class="mb-1">Co-payers</h2>
          <p class="mb-4 text-gray-500">Up to 5 people who can also pay this rent. Their names print as &ldquo;OR&rdquo; names on the schedule.</p>
          <ul v-if="active.payers.length" class="mb-4 divide-y divide-border-subtle rounded-sm border border-border-default">
            <li v-for="p in active.payers" :key="p.id" class="flex items-center justify-between gap-3 px-4 py-3">
              <span><span class="font-semibold">{{ p.name }}</span> <span v-if="p.phone" class="tnum ml-2 text-gray-500">{{ p.phone }}</span></span>
              <UiButton variant="icon" :label="`Remove ${p.name}`" @click="removePayer(p.id)"><Icon name="lucide:trash-2" class="size-5" /></UiButton>
            </li>
          </ul>
          <p v-else class="mb-4 text-gray-500">No co-payers.</p>
          <form v-if="active.payers.length < 5" class="grid items-start gap-3 sm:grid-cols-[1fr_1fr_auto]" novalidate @submit.prevent="addPayer">
            <UiField label="Name" for="cp-name" :error="pSub.fields.value.name"><UiInput id="cp-name" v-model="payer.name" /></UiField>
            <UiField label="Phone" for="cp-phone" optional :error="pSub.fields.value.phone"><UiInput id="cp-phone" v-model="payer.phone" type="tel" /></UiField>
            <div class="sm:pt-[26px]"><UiButton type="submit" :loading="pSub.loading.value" :disabled="!payer.name.trim()">Add</UiButton></div>
          </form>
          <UiFormAlert v-if="pSub.message.value" class="mt-3">{{ pSub.message.value }}</UiFormAlert>
        </UiCard>

        <!-- History -->
        <UiCard v-if="history.length" :padded="false">
          <h2 class="px-6 pt-6 pb-4">Lease history</h2>
          <div class="overflow-x-auto">
            <table class="w-full text-left">
              <thead><tr class="h-10 border-y border-border-default bg-gray-50"><th class="th-text px-5 font-medium">Tenant</th><th class="th-text px-5 font-medium">From</th><th class="th-text px-5 font-medium">To</th><th class="th-text px-5 text-right font-medium">Rent</th></tr></thead>
              <tbody>
                <tr v-for="l in history" :key="l.id" class="h-12 border-b border-border-subtle">
                  <td class="px-5 font-semibold">{{ l.tenant_name }}</td><td class="px-5">{{ formatDate(l.start_date) }}</td><td class="px-5">{{ l.end_date ? formatDate(l.end_date) : '—' }}</td>
                  <td class="money px-5 text-right">{{ formatMoney(l.rent_amount) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </UiCard>
      </template>

      <LedgerPanel v-else :key="tab" :unit-id="unitId" :kind="tab as 'rent' | 'water' | 'garbage' | 'electricity_deposit'" />

      <PropertyLeaseFormModal v-model="leaseModal" :unit-id="unitId" :lease="leaseEditing" :electricity-enabled="props_.current?.electricity_enabled" :electricity-deposit-default="props_.current?.electricity_deposit_amount" @saved="load" />

      <UiModal v-model="editUnit" title="Edit unit" width="sm">
        <form id="unit-edit" class="flex flex-col gap-5" novalidate @submit.prevent="saveUnit">
          <UiFormAlert v-if="uSub.message.value">{{ uSub.message.value }}</UiFormAlert>
          <UiField label="Unit code" for="eu-code" :error="uSub.fields.value.unit_code"><UiInput id="eu-code" v-model="uForm.unit_code" :invalid="!!uSub.fields.value.unit_code" /></UiField>
          <UiField label="Water meter number" for="eu-meter" optional :error="uSub.fields.value.meter_number"><UiInput id="eu-meter" v-model="uForm.meter_number" /></UiField>
        </form>
        <template #footer><UiButton variant="secondary" @click="editUnit = false">Cancel</UiButton><UiButton type="submit" form="unit-edit" :loading="uSub.loading.value" :disabled="!uForm.unit_code.trim()" @click="saveUnit">Save</UiButton></template>
      </UiModal>

      <UiConfirmDialog v-model="terminating" title="Terminate this lease?" :text="`${active?.tenant_name} will move out and the unit becomes vacant. The ledger history is kept.`" confirm-label="Terminate lease" destructive :loading="term.loading.value" @confirm="terminate">
        <UiFormAlert v-if="term.message.value" class="mt-4">{{ term.message.value }}</UiFormAlert>
      </UiConfirmDialog>
    </template>
  </div>
</template>
