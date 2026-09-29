<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { ImportRowError } from '~/types/api'
import type { OnboardSummary } from '~/types/onboarding'
import { ONBOARD_COLUMNS, templateHref } from '~/utils/onboardTemplate'

// One spreadsheet brings a building in: units, tenants, deposits already held,
// and arrears owed at go-live. Check first (nothing is written), then import.
definePageMeta({ title: 'Bring in your tenants' })

const route = useRoute()
const store = useOnboarding()
const properties = useProperties()
const toast = useToast()
const cur = useCurrency()

const propertyId = ref(String(route.query.property ?? ''))
const asAt = ref(new Date().toISOString().slice(0, 10))
const file = ref<File | null>(null)
const state = ref<'pick' | 'checking' | 'ready' | 'rejected' | 'importing' | 'done'>('pick')
const summary = ref<OnboardSummary | null>(null)
const problems = ref<ImportRowError[]>([])
const general = ref('')
const showColumns = ref(false)

onMounted(async () => {
  if (!properties.list.length) await properties.fetchList().catch(toast.fail)
  if (!propertyId.value && properties.list.length === 1) propertyId.value = properties.list[0]!.id
})
const property = computed(() => properties.list.find(p => p.id === propertyId.value))
const canCheck = computed(() => !!propertyId.value && !!file.value && !!asAt.value)

function pick(e: Event) {
  file.value = (e.target as HTMLInputElement).files?.[0] ?? null
  reset()
}
function reset() { state.value = 'pick'; summary.value = null; problems.value = []; general.value = '' }

function fail(e: unknown) {
  summary.value = null
  state.value = 'rejected'
  if (e instanceof ApiError && Array.isArray(e.details?.rows)) { problems.value = e.details.rows as ImportRowError[]; general.value = e.message }
  else general.value = e instanceof Error ? e.message : 'The file could not be checked.'
}

// Step 1: check. The API reports what WOULD happen and writes nothing.
async function check() {
  if (!canCheck.value) return
  state.value = 'checking'
  problems.value = []
  general.value = ''
  try {
    const r = await store.importSheet(propertyId.value, file.value!, { dryRun: true, asAt: asAt.value })
    summary.value = r.summary
    state.value = 'ready'
  } catch (e) { fail(e) }
}

// Step 2: import, all rows or none.
async function doImport() {
  if (!file.value) return
  state.value = 'importing'
  try {
    const r = await store.importSheet(propertyId.value, file.value, { dryRun: false, asAt: asAt.value })
    summary.value = r.summary
    state.value = 'done'
    toast.success(`${r.summary.leases_created} tenants brought in.`)
    store.fetchStatus().catch(() => {})
  } catch (e) { fail(e) }
}

const tenants = computed(() => summary.value?.leases_created ?? 0)
const owedTotal = computed(() => sumMoney([summary.value?.rent_arrears, summary.value?.water_arrears, summary.value?.garbage_arrears]))
const heldTotal = computed(() => sumMoney([summary.value?.rent_deposits_held, summary.value?.water_deposits_held]))
</script>

<template>
  <div class="mx-auto flex max-w-3xl flex-col gap-6">
    <NuxtLink to="/onboarding" class="inline-flex items-center gap-1 text-gray-500 hover:text-gray-900"><Icon name="lucide:chevron-left" class="size-4" />Setup guide</NuxtLink>

    <!-- Done -->
    <UiCard v-if="state === 'done' && summary">
      <div class="flex flex-col items-center gap-3 text-center">
        <span class="flex size-12 items-center justify-center rounded-full bg-paid-tint text-paid-text"><Icon name="lucide:check" class="size-6" /></span>
        <h2>{{ tenants }} tenants are in</h2>
        <p class="max-w-md text-gray-700">{{ summary.units_created }} new units, {{ summary.vacant_units }} vacant. Their deposits are recorded as held and their arrears as owed. Nothing was recorded as paid today.</p>
        <div class="mt-2 flex flex-wrap justify-center gap-3">
          <UiButton :to="`/properties/${propertyId}`">Open {{ property?.name ?? 'the property' }}</UiButton>
          <UiButton variant="secondary" :to="`/properties/${propertyId}?tab=water`">Enter starting meter readings</UiButton>
          <UiButton variant="secondary" to="/onboarding">Back to the setup guide</UiButton>
        </div>
      </div>
    </UiCard>

    <template v-else>
      <UiCard>
        <h2>1. Choose the property and the date</h2>
        <div class="mt-5 grid gap-5 sm:grid-cols-2">
          <UiField label="Property" for="ob-prop">
            <UiSelect id="ob-prop" v-model="propertyId" placeholder="Choose a property" :options="properties.list.map(p => ({ value: p.id, label: p.name }))" @update:model-value="reset" />
          </UiField>
          <UiField label="Figures are as at" for="ob-asat" help="Use the day you go live, usually the 1st of the month.">
            <UiInput id="ob-asat" v-model="asAt" type="date" @update:model-value="reset" />
          </UiField>
        </div>
        <UiFormAlert v-if="!properties.list.length" tone="info" class="mt-4">You need a property first. <NuxtLink to="/properties" class="font-semibold text-accent-text">Add one</NuxtLink>, then come back.</UiFormAlert>
      </UiCard>

      <UiCard>
        <h2>2. Fill in the spreadsheet</h2>
        <p class="mt-1 text-gray-700">One row per unit. Copy your current tenant list into our template, add each tenant's deposits held and what they owe today.</p>
        <div class="mt-4 flex flex-wrap gap-3">
          <a :href="templateHref()" download="willcoll-onboarding-template.csv" class="inline-flex h-10 items-center gap-2 rounded-sm border border-border-default bg-white px-5 font-semibold hover:bg-gray-50"><Icon name="lucide:download" class="size-4" />Download the template</a>
          <UiButton variant="secondary" @click="showColumns = !showColumns"><Icon :name="showColumns ? 'lucide:chevron-up' : 'lucide:list'" class="size-4" />{{ showColumns ? 'Hide' : 'What goes in each column' }}</UiButton>
        </div>
        <div v-if="showColumns" class="mt-4 overflow-x-auto rounded-sm border border-border-default">
          <table class="w-full text-left">
            <thead><tr class="h-10 bg-gray-50"><th class="th-text px-4 font-medium">Column</th><th class="th-text px-4 font-medium">Needed?</th><th class="th-text px-4 font-medium">What to put</th></tr></thead>
            <tbody>
              <tr v-for="c in ONBOARD_COLUMNS" :key="c.key" class="border-t border-border-subtle align-top">
                <td class="px-4 py-2"><code class="rounded-sm bg-gray-100 px-1">{{ c.key }}</code></td><td class="px-4 py-2 whitespace-nowrap">{{ c.required }}</td><td class="px-4 py-2 text-gray-700">{{ c.help }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <UiFormAlert tone="info" class="mt-4">Deposits you enter are money the tenant has <strong>already paid</strong>. They are recorded as held and never appear as a payment in your reports.</UiFormAlert>
      </UiCard>

      <UiCard>
        <h2>3. Upload and check</h2>
        <UiField label="Your spreadsheet (CSV)" for="ob-file" class="mt-4" help="Save your Excel sheet as CSV first. Up to 500 rows.">
          <input id="ob-file" type="file" accept=".csv,text/csv" class="block w-full rounded-sm border border-border-default bg-white p-2 file:mr-4 file:rounded-sm file:border-0 file:bg-gray-100 file:px-4 file:py-2 file:font-semibold" @change="pick">
        </UiField>
        <div class="mt-5"><UiButton :loading="state === 'checking'" :disabled="!canCheck" @click="check"><Icon name="lucide:search-check" class="size-4" />Check my spreadsheet</UiButton></div>
        <p class="mt-2 text-gray-500">Checking does not save anything.</p>

        <!-- Rejected: every problem by row -->
        <div v-if="state === 'rejected'" class="mt-6 flex flex-col gap-4">
          <UiFormAlert>{{ general }}</UiFormAlert>
          <div v-if="problems.length" class="max-h-72 overflow-auto rounded-sm border border-border-default">
            <table class="w-full text-left">
              <thead class="sticky top-0 bg-gray-50"><tr class="h-10"><th class="th-text px-4 font-medium">Row</th><th class="th-text px-4 font-medium">Column</th><th class="th-text px-4 font-medium">Problem</th></tr></thead>
              <tbody><tr v-for="(p, i) in problems" :key="i" class="border-t border-border-subtle align-top"><td class="tnum px-4 py-2 font-semibold">{{ p.row }}</td><td class="px-4 py-2">{{ p.field }}</td><td class="px-4 py-2">{{ p.message }}</td></tr></tbody>
            </table>
          </div>
          <p class="text-gray-500">Fix these in your sheet and choose the file again. Row numbers match the lines in your file, counting the header as row 1.</p>
        </div>

        <!-- Ready: what will happen -->
        <div v-if="(state === 'ready' || state === 'importing') && summary" class="mt-6 flex flex-col gap-5" data-testid="import-preview">
          <UiFormAlert tone="success">The spreadsheet is valid. Here is what will happen when you import it.</UiFormAlert>
          <section class="grid grid-cols-2 gap-4 lg:grid-cols-4" aria-label="Import summary">
            <UiKpiTile label="Tenants" :value="String(summary.leases_created)" :delta="`${summary.units_created} new units`" />
            <UiKpiTile label="Vacant units" :value="String(summary.vacant_units)" />
            <UiKpiTile label="Monthly rent" :value="`Ksh ${cur.format(summary.monthly_rent)}`" />
            <UiKpiTile label="Owed today" :value="`Ksh ${cur.format(owedTotal)}`" tone="arrears" :delta="`Rent ${cur.format(summary.rent_arrears)} · Water ${cur.format(summary.water_arrears)} · Garbage ${cur.format(summary.garbage_arrears)}`" />
          </section>
          <ul class="list-disc space-y-1 pl-5 text-gray-700">
            <li>Deposits already held: <strong class="money">Ksh {{ cur.format(heldTotal) }}</strong> (rent {{ cur.format(summary.rent_deposits_held) }}, water {{ cur.format(summary.water_deposits_held) }}). Recorded as held, not as payments.</li>
            <li>What tenants owe is recorded as <strong>Balance brought forward</strong> as at {{ formatDate(asAt) }}. It shows in each unit's ledger and on their next bill.</li>
            <li>It is all or nothing. If anything goes wrong, nothing is saved.</li>
            <li>A mistake can be corrected afterwards with a reversal on the unit's ledger.</li>
          </ul>
          <div><UiButton :loading="state === 'importing'" @click="doImport"><Icon name="lucide:upload" class="size-4" />Import {{ summary.leases_created }} tenants</UiButton></div>
        </div>
      </UiCard>
    </template>
  </div>
</template>
