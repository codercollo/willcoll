<script setup lang="ts">
import type { PbacAction, PropertyGrant } from '~/composables/usePermissions'

definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()
const permissions = usePermissions()

interface Agent { id: string; full_name: string; phone: string; email: string; status: string }
interface Property { id: string; name: string }

const PBAC_FIELDS: { key: PbacAction; label: string }[] = [
  { key: 'can_record_payments', label: 'Record payments' },
  { key: 'can_edit_leases', label: 'Edit leases' },
  { key: 'can_edit_unit_pricing', label: 'Edit unit pricing' },
  { key: 'can_void_payments', label: 'Void payments' },
  { key: 'can_view_financial_reports', label: 'View financial reports' },
  { key: 'can_manage_meter_readings', label: 'Manage meter readings' },
]

const agents = ref<Agent[]>([])
const properties = ref<Property[]>([])
const status = ref<'loading' | 'ready' | 'empty' | 'error'>('loading')
const error = ref('')

const showInvite = ref(false)
const inviteForm = ref({ full_name: '', phone: '', email: '' })
const inviting = ref(false)
const inviteError = ref('')

const resettingID = ref<string | null>(null)
const resetMessage = ref<Record<string, string>>({})

const selectedAgentID = ref<string | null>(null)
const grants = ref<Record<string, PropertyGrant>>({})
const grantsLoading = ref(false)
const grantsError = ref('')
const savingCell = ref<string | null>(null)

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
    const [agentsResponse, propertiesResponse] = await Promise.all([
      api.request<{ data: Agent[] }>('/v1/agents'),
      api.request<{ data: Property[] }>('/v1/properties'),
    ])
    agents.value = agentsResponse.data
    properties.value = propertiesResponse.data
    status.value = agents.value.length ? 'ready' : 'empty'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load agents.'
    status.value = 'error'
  }
}

async function inviteAgent() {
  inviteError.value = ''
  inviting.value = true
  try {
    await api.request('/v1/agents', { method: 'POST', body: inviteForm.value })
    showInvite.value = false
    inviteForm.value = { full_name: '', phone: '', email: '' }
    await load()
  } catch (e: any) {
    inviteError.value = e?.data?.error ?? 'Unable to invite agent.'
  } finally {
    inviting.value = false
  }
}

async function resetPassword(agentID: string) {
  resettingID.value = agentID
  resetMessage.value = { ...resetMessage.value, [agentID]: '' }
  try {
    await api.request(`/v1/agents/${agentID}/reset-password`, { method: 'POST' })
    resetMessage.value = { ...resetMessage.value, [agentID]: 'Reset email sent.' }
  } catch (e: any) {
    resetMessage.value = { ...resetMessage.value, [agentID]: e?.data?.error ?? 'Unable to send reset email.' }
  } finally {
    resettingID.value = null
  }
}

function emptyGrant(propertyID: string): PropertyGrant {
  return {
    property_id: propertyID,
    can_record_payments: false,
    can_edit_leases: false,
    can_edit_unit_pricing: false,
    can_void_payments: false,
    can_view_financial_reports: false,
    can_manage_meter_readings: false,
  }
}

async function openGrants(agentID: string) {
  selectedAgentID.value = agentID
  grantsError.value = ''
  grantsLoading.value = true
  grants.value = Object.fromEntries(properties.value.map((p) => [p.id, emptyGrant(p.id)]))
  try {
    const response = await api.request<{ data: Array<PropertyGrant & { property_id: string }> }>(
      `/v1/agent-grants?agent_id=${agentID}`,
    )
    for (const row of response.data) {
      grants.value[row.property_id] = { ...emptyGrant(row.property_id), ...row }
    }
  } catch (e: any) {
    grantsError.value = e?.data?.error ?? 'Unable to load existing grants.'
  } finally {
    grantsLoading.value = false
  }
}

async function toggleGrant(propertyID: string, field: PbacAction) {
  if (!selectedAgentID.value) return
  const current = grants.value[propertyID] ?? emptyGrant(propertyID)
  const next = { ...current, [field]: !current[field] }
  grants.value = { ...grants.value, [propertyID]: next }

  savingCell.value = `${propertyID}:${field}`
  try {
    await api.request('/v1/agent-grants', {
      method: 'POST',
      body: {
        agent_id: selectedAgentID.value,
        property_id: propertyID,
        can_record_payments: next.can_record_payments,
        can_edit_leases: next.can_edit_leases,
        can_edit_unit_pricing: next.can_edit_unit_pricing,
        can_void_payments: next.can_void_payments,
        can_view_financial_reports: next.can_view_financial_reports,
        can_manage_meter_readings: next.can_manage_meter_readings,
      },
    })
  } catch (e: any) {
    // Revert on failure — the server never saw the change.
    grants.value = { ...grants.value, [propertyID]: current }
    grantsError.value = e?.data?.error ?? 'Unable to update grant.'
  } finally {
    savingCell.value = null
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Agents</h1>
      <button type="button" class="primary-button" @click="showInvite = !showInvite">
        {{ showInvite ? 'Cancel' : 'Invite agent' }}
      </button>
    </div>

    <form v-if="showInvite" class="create-form" @submit.prevent="inviteAgent">
      <label class="field">
        <span>Full name</span>
        <input v-model="inviteForm.full_name" type="text" required>
      </label>
      <label class="field">
        <span>Phone</span>
        <input v-model="inviteForm.phone" type="text" required>
      </label>
      <label class="field">
        <span>Email</span>
        <input v-model="inviteForm.email" type="email" required>
      </label>
      <p v-if="inviteError" class="page-error">{{ inviteError }}</p>
      <button type="submit" class="primary-button" :disabled="inviting">
        {{ inviting ? 'Sending invite...' : 'Send invite' }}
      </button>
    </form>

    <p v-if="status === 'loading'" class="page-state">Loading agents&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>
    <p v-else-if="status === 'empty'" class="page-state">No agents yet.</p>

    <table v-else class="agents-table">
      <thead>
        <tr>
          <th>Name</th>
          <th>Phone</th>
          <th>Email</th>
          <th>Status</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <template v-for="agent in agents" :key="agent.id">
          <tr>
            <td>{{ agent.full_name }}</td>
            <td>{{ agent.phone }}</td>
            <td>{{ agent.email }}</td>
            <td>{{ agent.status }}</td>
            <td class="row-actions">
              <button
                type="button"
                class="link-button"
                @click="selectedAgentID === agent.id ? (selectedAgentID = null) : openGrants(agent.id)"
              >
                {{ selectedAgentID === agent.id ? 'Hide grants' : 'Edit grants' }}
              </button>
              <button
                type="button"
                class="link-button"
                :disabled="resettingID === agent.id"
                @click="resetPassword(agent.id)"
              >
                {{ resettingID === agent.id ? 'Sending...' : 'Reset password' }}
              </button>
              <span v-if="resetMessage[agent.id]" class="hint">{{ resetMessage[agent.id] }}</span>
            </td>
          </tr>
          <tr v-if="selectedAgentID === agent.id">
            <td colspan="5">
              <div class="grant-matrix">
                <p v-if="grantsLoading" class="page-state">Loading grants&hellip;</p>
                <p v-else-if="grantsError" class="page-error">{{ grantsError }}</p>
                <p v-else-if="!properties.length" class="page-state">No properties yet.</p>
                <table v-else class="matrix-table">
                  <thead>
                    <tr>
                      <th>Property</th>
                      <th v-for="field in PBAC_FIELDS" :key="field.key">{{ field.label }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="property in properties" :key="property.id">
                      <td>{{ property.name }}</td>
                      <td v-for="field in PBAC_FIELDS" :key="field.key" class="checkbox-cell">
                        <input
                          type="checkbox"
                          :checked="grants[property.id]?.[field.key] ?? false"
                          :disabled="savingCell === `${property.id}:${field.key}`"
                          @change="toggleGrant(property.id, field.key)"
                        >
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
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

.create-form {
  display: grid;
  gap: var(--space-3);
  max-width: 420px;
  padding: var(--space-4);
  margin-bottom: var(--space-6);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

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

.agents-table, .matrix-table {
  width: 100%;
  border-collapse: collapse;
}

.agents-table th, .agents-table td,
.matrix-table th, .matrix-table td {
  text-align: left;
  padding: var(--space-2) var(--space-3);
  border-bottom: var(--border-hairline, 1px solid #E2E5E9);
}

.row-actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.link-button {
  border: 0;
  background: none;
  padding: 0;
  color: var(--color-action-primary, #E8702A);
  font-weight: 600;
  cursor: pointer;
}
.link-button:disabled { opacity: 0.6; cursor: default; }

.hint { color: var(--color-text-muted, #6B7280); font-size: 0.875rem; }

.grant-matrix {
  padding: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
}

.checkbox-cell { text-align: center; }
</style>
