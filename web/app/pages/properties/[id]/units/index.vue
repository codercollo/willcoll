<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const permissions = usePermissions()
const route = useRoute()

const propertyID = computed(() => String(route.params.id))
const units = ref<Array<{
  id: string
  unit_label: string
  status: string
  base_rent: number
}>>([])
const error = ref('')

async function load() {
  if (!auth.token.value) {
    await navigateTo('/login')
    return
  }
  try {
    const response = await $fetch<{ data: Array<{ id: string; unit_label: string; status: string; base_rent: number }> }>(
      `/v1/properties/${propertyID.value}/units`,
      { headers: { Authorization: `Bearer ${auth.token.value}` } },
    )
    units.value = response.data
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load units.'
  }
}

onMounted(load)
</script>

<template>
  <div>
    <h1 class="page-title">Units</h1>
    <p v-if="error" class="page-error">{{ error }}</p>

    <section class="unit-list">
      <div v-for="unit in units" :key="unit.id" class="unit-row">
        <div class="unit-row__label">{{ unit.unit_label }}</div>
        <div class="unit-row__tenant">Tenant name unavailable</div>
        <StatusBadge :label="unit.status" :tone="unit.status === 'occupied' ? 'success' : 'warning'" />
        <div class="unit-row__balance">KES {{ unit.base_rent.toLocaleString() }}</div>
        <button
          v-if="permissions.canRecordPayments(propertyID)"
          type="button"
          class="primary-button"
        >
          Record Payment
        </button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.page-title {
  margin: 0 0 var(--space-8);
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
}

.page-error { color: var(--color-status-error, #C6362E); }

.unit-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.unit-row {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4);
  background: var(--surface-panel, #FFFFFF);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.unit-row__label { font-weight: 600; }
.unit-row__tenant { color: var(--color-text-muted, #6B7280); }
.unit-row__balance { margin-left: auto; font-variant-numeric: tabular-nums; }

.primary-button {
  padding: var(--space-2) var(--space-3);
  border: 0;
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
  color: #FFFFFF;
  font-weight: 600;
  cursor: pointer;
}
</style>
