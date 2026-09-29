<script setup lang="ts">
definePageMeta({ title: 'Properties' })

const store = useProperties()
const toast = useToast()
const loading = ref(true)
const error = ref('')
const creating = ref(false)
const period = currentPeriod()

async function load() {
  loading.value = true
  error.value = ''
  try { await store.fetchList(period) } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load properties.' } finally { loading.value = false }
}
onMounted(load)

// Portfolio totals are sums of what the API already reports per property.
const totals = computed(() => ({
  occupied: store.list.reduce((a, p) => a + (p.summary?.units_occupied ?? 0), 0),
  vacant: store.list.reduce((a, p) => a + (p.summary?.units_vacant ?? 0), 0),
  expected: formatMoney(sumMoney(store.list.map(p => p.summary?.rent_expected))),
  collected: formatMoney(sumMoney(store.list.map(p => p.summary?.rent_collected))),
}))

async function created(p: { id: string }) {
  toast.info('Add units or import a CSV to get started.')
  await navigateTo(`/properties/${p.id}`)
}
</script>

<template>
  <div class="flex flex-col gap-8">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <p class="text-gray-500">{{ periodLabel(period) }}</p>
      <div class="flex gap-3">
        <UiButton variant="secondary" to="/landlords"><Icon name="lucide:users" class="size-4" />Landlords</UiButton>
        <UiButton @click="creating = true"><Icon name="lucide:plus" class="size-4" />New property</UiButton>
      </div>
    </div>

    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>

    <div v-if="loading" class="grid grid-cols-1 gap-6 lg:grid-cols-2 xl:grid-cols-3"><UiSkeleton v-for="i in 3" :key="i" block /></div>

    <template v-else-if="!error">
      <section v-if="store.list.length" class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <UiKpiTile label="Properties" :value="String(store.list.length)" />
        <UiKpiTile label="Units occupied" :value="`${totals.occupied} / ${totals.occupied + totals.vacant}`" />
        <UiKpiTile label="Rent expected" :value="`Ksh ${totals.expected}`" />
        <UiKpiTile label="Rent collected" :value="`Ksh ${totals.collected}`" />
      </section>

      <div v-if="store.list.length" class="grid grid-cols-1 gap-6 lg:grid-cols-2 xl:grid-cols-3">
        <PropertyCard v-for="p in store.list" :key="p.id" :property="p" />
      </div>

      <UiCard v-else :padded="false">
        <UiEmptyState title="No properties yet" text="Add a landlord and your first property to start billing." icon="lucide:building-2">
          <div class="flex flex-wrap justify-center gap-3"><UiButton @click="creating = true">New property</UiButton><UiButton variant="secondary" to="/onboarding">Follow the setup guide</UiButton></div>
        </UiEmptyState>
      </UiCard>
    </template>

    <PropertyFormModal v-model="creating" @created="created" />
  </div>
</template>
