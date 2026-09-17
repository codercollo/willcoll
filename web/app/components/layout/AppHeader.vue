<script setup lang="ts">
const branding = useBranding()
const api = useApi()

interface PropertyOption { id: string; name: string }

const properties = ref<PropertyOption[]>([])
const propertiesStatus = ref<'loading' | 'ready' | 'error'>('loading')
const currentPropertyId = useState<string | null>('current-property-id', () => null)

// "Collected this month" is real KES from /v1/reports/collections — there's
// no amount-due aggregate exposed anywhere the header can honestly divide
// by, so this stays an absolute figure rather than a fabricated percentage
// (no endpoint currently supports a true collection-rate %).
const collectedThisMonth = ref<number | null>(null)
const arrearsCount = ref<number | null>(null)
const metricsStatus = ref<'loading' | 'ready' | 'error'>('loading')

async function loadProperties() {
  propertiesStatus.value = 'loading'
  try {
    const response = await api.request<{ data: PropertyOption[] }>('/v1/properties')
    properties.value = response.data
    propertiesStatus.value = 'ready'
  } catch {
    propertiesStatus.value = 'error'
  }
}

async function loadMetrics() {
  metricsStatus.value = 'loading'
  const propertyQuery = currentPropertyId.value ? `&property_id=${currentPropertyId.value}` : ''
  const month = new Date().toISOString().slice(0, 7)
  try {
    const [collections, arrears] = await Promise.all([
      api.request<{ data: Array<{ collected: string | number }> }>(`/v1/reports/collections?month=${month}${propertyQuery}`),
      api.request<{ data: Array<{ arrears: string | number }> }>(`/v1/reports/arrears?${propertyQuery.replace(/^&/, '')}`),
    ])
    collectedThisMonth.value = collections.data.reduce((sum, row) => sum + Number(row.collected), 0)
    arrearsCount.value = arrears.data.filter((row) => Number(row.arrears) > 0).length
    metricsStatus.value = 'ready'
  } catch {
    metricsStatus.value = 'error'
  }
}

onMounted(() => {
  loadProperties()
  loadMetrics()
})
watch(currentPropertyId, loadMetrics)
</script>

<template>
  <header class="app-header">
    <div class="app-header__left">
      <img v-if="branding?.logo_url" :src="branding?.logo_url" :alt="branding?.brand_name" class="app-header__logo">
      <span v-else class="app-header__name">{{ branding?.brand_name }}</span>

      <select
        v-model="currentPropertyId"
        class="property-switcher"
        :disabled="propertiesStatus === 'loading'"
      >
        <option :value="null">All properties</option>
        <option v-for="p in properties" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
      <span v-if="propertiesStatus === 'error'" class="header-error">Could not load properties.</span>
    </div>

    <div class="app-header__right">
      <template v-if="metricsStatus === 'ready'">
        <span class="collection-chip">Collected: KES {{ (collectedThisMonth ?? 0).toLocaleString() }}</span>
        <button class="bell" :aria-label="`${arrearsCount ?? 0} properties with arrears`" type="button">
          <span class="bell-count">{{ arrearsCount ?? 0 }}</span>
        </button>
      </template>
      <span v-else-if="metricsStatus === 'loading'" class="header-loading">Loading&hellip;</span>
      <span v-else class="header-error">Could not load metrics.</span>
    </div>
  </header>
</template>

<style scoped>
.app-header {
  min-height: var(--layout-header-height, 68px);
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  background: var(--surface-panel, #FFFFFF);
  border-bottom: var(--border-hairline, 1px solid #E2E5E9);
}

.app-header__left,
.app-header__right {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.app-header__logo { max-height: 32px; }
.app-header__name { font-family: var(--font-display, serif); color: var(--color-text-primary, #1F2320); }

.property-switcher {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  max-width: 40vw;
}

.header-error { color: var(--color-status-error, #C6362E); font-size: var(--text-ui-sm); }
.header-loading { color: var(--color-text-muted, #6B7280); font-size: var(--text-ui-sm); }

.collection-chip {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm, 4px);
  background: var(--color-status-success-bg, rgba(30,142,90,0.12));
  color: var(--color-status-success, #1E8E5A);
  white-space: nowrap;
}

.bell {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 24px;
  height: 24px;
  padding: 0 var(--space-1);
  border: 0;
  border-radius: 999px;
  background: var(--color-status-warning-bg, rgba(201,130,26,0.12));
  color: var(--color-status-warning, #C9821A);
  font-size: var(--text-ui-xs);
  font-weight: 600;
  cursor: pointer;
}

@media (max-width: 640px) {
  .property-switcher { max-width: 100%; }
}
</style>
