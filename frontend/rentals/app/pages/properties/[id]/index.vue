<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { BulkQrSummary } from '~/stores/qr'

definePageMeta({ title: 'Property' })

const route = useRoute()
const store = useProperties()
const units = useUnits()
const qr = useQr()
const toast = useToast()
const id = computed(() => String(route.params.id))

// ---- header ---------------------------------------------------------------
const loading = ref(true)
const notFound = ref(false)
const error = ref('')
const property = computed(() => (store.current?.id === id.value ? store.current : null))

async function loadProperty() {
  loading.value = true
  error.value = ''
  notFound.value = false
  try {
    const p = await store.fetchOne(id.value)
    route.meta.title = p.name
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else error.value = e instanceof Error ? e.message : 'Could not load the property.'
  } finally { loading.value = false }
}
onMounted(loadProperty)
watch(id, loadProperty)

// ---- tabs (?tab=) ---------------------------------------------------------
const tabs = [
  { key: 'units', label: 'Units' }, { key: 'rent', label: 'Rent' }, { key: 'water', label: 'Water' },
  { key: 'garbage', label: 'Garbage' }, { key: 'electricity', label: 'Electricity' },
  { key: 'reports', label: 'Reports' }, { key: 'settings', label: 'Settings' },
]
const tab = computed({
  get: () => (tabs.some(t => t.key === route.query.tab) ? String(route.query.tab) : 'units'),
  set: (v: string) => { navigateTo({ query: { ...route.query, tab: v } }, { replace: true }) },
})
const visibleTabs = computed(() => tabs.filter(t =>
  (t.key !== 'garbage' || property.value?.garbage_enabled !== false)
  && (t.key !== 'electricity' || property.value?.electricity_enabled === true)))

// ---- units tab ------------------------------------------------------------
const search = ref('')
const status = ref('')
const page = ref(1)
const unitsLoading = ref(false)
const unitsError = ref('')
const adding = ref(false)
const importing = ref(false)

async function loadUnits() {
  unitsLoading.value = true
  unitsError.value = ''
  try { await units.fetchList(id.value, { search: search.value.trim() || undefined, status: status.value || undefined, page: page.value }) }
  catch (e) { unitsError.value = e instanceof Error ? e.message : 'Could not load units.' }
  finally { unitsLoading.value = false }
}
let debounce: ReturnType<typeof setTimeout>
watch([search, status], () => { clearTimeout(debounce); debounce = setTimeout(() => { page.value = 1; loadUnits() }, 250) })
watch(page, loadUnits)
onMounted(loadUnits)
onBeforeUnmount(() => clearTimeout(debounce))

async function refreshAll() { await Promise.all([loadUnits(), store.fetchOne(id.value)]) }

// ---- bulk QR select mode ---------------------------------------------------
const selectMode = ref(false)
const selectedLeaseIds = ref(new Set<string>())
const selectableUnits = computed(() => units.list.filter(u => u.current_lease))
const allSelected = computed(() => selectableUnits.value.length > 0 && selectableUnits.value.every(u => selectedLeaseIds.value.has(u.current_lease!.lease_id)))

function toggleSelectMode() {
  selectMode.value = !selectMode.value
  selectedLeaseIds.value = new Set()
  bulkSummary.value = null
}
function toggleUnit(leaseId: string) {
  const next = new Set(selectedLeaseIds.value)
  if (next.has(leaseId)) next.delete(leaseId)
  else next.add(leaseId)
  selectedLeaseIds.value = next
}
function selectAllOccupied() {
  selectedLeaseIds.value = allSelected.value ? new Set() : new Set(selectableUnits.value.map(u => u.current_lease!.lease_id))
}

const generatingQr = ref(false)
const bulkSummary = ref<BulkQrSummary | null>(null)
async function generateStickers() {
  if (!selectedLeaseIds.value.size) return
  generatingQr.value = true
  bulkSummary.value = null
  try {
    const { blob, summary } = await qr.bulk(id.value, [...selectedLeaseIds.value])
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'unit-qr-stickers.pdf'
    a.click()
    URL.revokeObjectURL(url)
    bulkSummary.value = summary
    toast.success(`${summary.created + summary.reused} sticker(s) ready (${summary.created} new, ${summary.reused} reused).`)
    await loadUnits()
  } catch (e) { toast.fail(e) } finally { generatingQr.value = false }
}

const s = computed(() => property.value?.summary)
</script>

<template>
  <div class="flex flex-col gap-6">
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="loadProperty">Retry</button></UiFormAlert>
    <UiCard v-if="notFound" :padded="false"><UiEmptyState title="Property not found" text="It may have been archived, or it is not yours." icon="lucide:building-2"><UiButton to="/properties">Back to properties</UiButton></UiEmptyState></UiCard>
    <UiSkeleton v-else-if="loading && !property" block />

    <template v-else-if="property">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <NuxtLink to="/properties" class="mb-2 inline-flex items-center gap-1 text-gray-500 hover:text-gray-900"><Icon name="lucide:chevron-left" class="size-4" />Properties</NuxtLink>
          <p class="text-gray-500">{{ s?.landlord_name }} &middot; {{ property.location }}</p>
        </div>
        <div v-if="s" class="flex flex-wrap gap-6">
          <div><p class="text-[length:var(--text-caption)] text-gray-500">Occupied</p><p class="tnum text-[length:var(--text-money-lg)] font-bold">{{ s.units_occupied }} / {{ s.units_occupied + s.units_vacant }}</p></div>
          <div><p class="text-[length:var(--text-caption)] text-gray-500">Rent collected ({{ periodLabel(s.period) }})</p><p class="money text-[length:var(--text-money-lg)] font-bold">{{ formatMoney(s.rent_collected) }} <span class="text-[length:var(--text-body)] font-normal text-gray-500">/ {{ formatMoney(s.rent_expected) }}</span></p></div>
        </div>
      </header>

      <UiTabs v-model="tab" :tabs="visibleTabs" />

      <!-- Units -->
      <section v-if="tab === 'units'" class="flex flex-col gap-6">
        <div class="flex flex-wrap items-center gap-3">
          <div class="w-full sm:w-72"><UiInput v-model="search" placeholder="Search tenant name" aria-label="Search tenants" /></div>
          <div class="w-40"><UiSelect v-model="status" aria-label="Status" :options="[{ value: '', label: 'All units' }, { value: 'occupied', label: 'Occupied' }, { value: 'vacant', label: 'Vacant' }]" /></div>
          <div class="ml-auto flex gap-3">
            <template v-if="selectMode">
              <UiButton variant="secondary" @click="selectAllOccupied">{{ allSelected ? 'Deselect all' : 'Select all occupied' }}</UiButton>
              <UiButton :loading="generatingQr" :disabled="!selectedLeaseIds.size" @click="generateStickers"><Icon name="lucide:qr-code" class="size-4" />Generate QR stickers ({{ selectedLeaseIds.size }})</UiButton>
              <UiButton variant="secondary" @click="toggleSelectMode">Cancel</UiButton>
            </template>
            <template v-else>
              <UiButton variant="secondary" @click="toggleSelectMode"><Icon name="lucide:qr-code" class="size-4" />Select for QR stickers</UiButton>
              <UiButton variant="secondary" :to="`/onboarding/import?property=${id}`"><Icon name="lucide:users" class="size-4" />Import tenants &amp; balances</UiButton>
              <UiButton variant="secondary" @click="importing = true"><Icon name="lucide:upload" class="size-4" />Import units only</UiButton>
              <UiButton @click="adding = true"><Icon name="lucide:plus" class="size-4" />Add unit</UiButton>
            </template>
          </div>
        </div>

        <UiFormAlert v-if="unitsError">{{ unitsError }} <button type="button" class="font-semibold text-accent-text" @click="loadUnits">Retry</button></UiFormAlert>
        <UiFormAlert v-if="bulkSummary" tone="success" class="flex flex-col gap-1">
          <span>{{ bulkSummary.created }} created, {{ bulkSummary.reused }} reused.</span>
          <span v-if="bulkSummary.skipped.length">{{ bulkSummary.skipped.length }} skipped: <span v-for="(s, i) in bulkSummary.skipped" :key="s.lease_id">{{ i > 0 ? '; ' : '' }}{{ s.reason }}</span></span>
        </UiFormAlert>
        <div v-if="unitsLoading && !units.list.length" class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"><UiSkeleton v-for="i in 8" :key="i" block /></div>
        <div v-else-if="units.list.length" class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          <PropertyUnitCard v-for="u in units.list" :key="u.id" :unit="u" :property-id="id" :select-mode="selectMode"
            :selected="!!u.current_lease && selectedLeaseIds.has(u.current_lease.lease_id)"
            @toggle="u.current_lease && toggleUnit(u.current_lease.lease_id)" />
        </div>
        <UiCard v-else-if="!unitsError" :padded="false">
          <UiEmptyState :title="search || status ? 'No units match' : 'No units yet'" :text="search || status ? 'Try a different search or filter.' : 'Add units one by one, or import a whole sheet.'" icon="lucide:door-open" />
        </UiCard>
        <UiPagination :meta="units.meta" @change="(p) => (page = p)" />
      </section>

      <UiCard v-else-if="tab === 'settings'" :padded="false" class="!shadow-none !bg-transparent">
        <PropertySettings :property="property" @reload="refreshAll" />
      </UiCard>

      <PropertyRentTab v-else-if="tab === 'rent'" :property-id="id" @changed="store.fetchOne(id)" />
      <PropertyWaterTab v-else-if="tab === 'water'" :property-id="id" @changed="store.fetchOne(id)" />
      <PropertyGarbageTab v-else-if="tab === 'garbage'" :property-id="id" @changed="store.fetchOne(id)" />
      <PropertyElectricityTab v-else-if="tab === 'electricity'" :property-id="id" />

      <PropertyReportsTab v-else-if="tab === 'reports'" :property-id="id" />

      <PropertyAddUnitModal v-model="adding" :property-id="id" @saved="refreshAll" />
      <PropertyImportUnitsModal v-model="importing" :property-id="id" @imported="refreshAll" />
    </template>
  </div>
</template>
