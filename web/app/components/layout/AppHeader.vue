<script setup lang="ts">
const branding = useBranding()

const currentPropertyId = useState<string | null>('current-property-id', () => null)
const arrearsTotal = ref(0)

async function refreshArrears() {
  const query = currentPropertyId.value ? `?property_id=${currentPropertyId.value}` : ''
  try {
    const response = await $fetch<{ data: Array<{ arrears: string | number }> }>(`/v1/reports/arrears${query}`)
    arrearsTotal.value = response.data.reduce((sum, row) => sum + Number(row.arrears), 0)
  } catch {
    arrearsTotal.value = 0
  }
}

onMounted(refreshArrears)
watch(currentPropertyId, refreshArrears)
</script>

<template>
  <header class="app-header">
    <div class="app-header__left">
      <img v-if="branding.logo_url" :src="branding.logo_url" :alt="branding.brand_name" class="app-header__logo">
      <span v-else class="app-header__name">{{ branding.brand_name }}</span>

      <select v-model="currentPropertyId" class="property-switcher">
        <option :value="null">All properties</option>
      </select>
    </div>

    <div class="app-header__right">
      <span class="collection-chip">
        Arrears: {{ arrearsTotal.toLocaleString() }}
      </span>
      <button class="bell" aria-label="Arrears alerts" type="button">●</button>
    </div>
  </header>
</template>

<style scoped>
.app-header {
  height: var(--layout-header-height, 68px);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-4);
  background: var(--surface-panel, #FFFFFF);
  border-bottom: var(--border-hairline, 1px solid #E2E5E9);
}

.app-header__left,
.app-header__right {
  display: flex;
  align-items: center;
  gap: var(--space-4);
}

.app-header__logo { max-height: 32px; }
.app-header__name { font-family: var(--font-display, serif); color: var(--color-text-primary, #1F2320); }

.property-switcher {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.collection-chip {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm, 4px);
  background: var(--color-status-warning-bg, rgba(201,130,26,0.12));
  color: var(--color-status-warning, #C9821A);
}

.bell {
  border: 0;
  background: transparent;
  color: var(--color-text-muted, #6B7280);
  cursor: pointer;
}
</style>
