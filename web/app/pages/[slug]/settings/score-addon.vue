<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()
const permissions = usePermissions()

interface AddonStatus { status: string; monthly_fee_kes: number }

const status = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
const addon = ref<AddonStatus | null>(null)
const monthlyFee = ref('')
const busy = ref(false)
const actionError = ref('')

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  if (!permissions.isManager.value) {
    await navigateTo(`/${route.params.slug}/properties`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: AddonStatus }>('/v1/organization/addons/verified-property-score')
    addon.value = response.data
    status.value = 'ready'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load add-on status.'
    status.value = 'error'
  }
}

async function activate() {
  actionError.value = ''
  busy.value = true
  try {
    await api.request('/v1/organization/addons/verified-property-score/activate', {
      method: 'POST',
      body: { monthly_fee_kes: Number(monthlyFee.value) },
    })
    await load()
  } catch (e: any) {
    actionError.value = e?.data?.error ?? 'Unable to activate add-on.'
  } finally {
    busy.value = false
  }
}

async function cancel() {
  actionError.value = ''
  busy.value = true
  try {
    await api.request('/v1/organization/addons/verified-property-score/cancel', { method: 'POST' })
    await load()
  } catch (e: any) {
    actionError.value = e?.data?.error ?? 'Unable to cancel add-on.'
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Verified Property Score</h1>
    </div>

    <p v-if="status === 'loading'" class="page-state">Loading&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>

    <div v-else class="addon-card">
      <p>
        Status:
        <StatusBadge :tone="addon?.status === 'active' ? 'success' : 'warning'" :label="addon?.status ?? 'inactive'" />
      </p>
      <p v-if="addon?.status === 'active'">Monthly fee: KES {{ addon.monthly_fee_kes }}</p>

      <form v-if="addon?.status !== 'active'" class="activate-form" @submit.prevent="activate">
        <label class="field">
          <span>Monthly fee (KES)</span>
          <input v-model="monthlyFee" type="number" min="1" step="0.01" required>
        </label>
        <button type="submit" class="primary-button" :disabled="busy">
          {{ busy ? 'Activating...' : 'Activate add-on' }}
        </button>
      </form>
      <button v-else type="button" class="secondary-button" :disabled="busy" @click="cancel">
        {{ busy ? 'Cancelling...' : 'Cancel add-on' }}
      </button>

      <p v-if="actionError" class="page-error">{{ actionError }}</p>
    </div>
  </div>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-6);
}

.page-title {
  margin: 0;
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
}

.page-error { color: var(--color-status-error, #C6362E); }
.page-state { color: var(--color-text-muted, #6B7280); }

.addon-card {
  display: grid;
  gap: var(--space-3);
  max-width: 420px;
  padding: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.activate-form { display: grid; gap: var(--space-3); }
.field { display: grid; gap: var(--space-1); }
.field input {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.primary-button {
  padding: var(--space-2) var(--space-4);
  border: 0;
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
  color: #FFFFFF;
  font-weight: 600;
  cursor: pointer;
  justify-self: start;
}
.primary-button:disabled { opacity: 0.7; cursor: default; }

.secondary-button {
  padding: var(--space-2) var(--space-4);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-md, 6px);
  background: var(--surface-card, #FFFFFF);
  cursor: pointer;
  justify-self: start;
}
.secondary-button:disabled { opacity: 0.7; cursor: default; }
</style>
