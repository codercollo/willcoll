<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()
const permissions = usePermissions()

interface SMSTemplate { key: string; actor: 'tenant' | 'landlord'; vars: string[]; body: string; override: boolean }

const TEMPLATE_LABELS: Record<string, string> = {
  tenant_rent_due: 'Rent due reminder',
  tenant_rent_received: 'Payment received receipt',
  tenant_arrears_notice: 'Arrears notice',
  tenant_welcome: 'Welcome message',
  landlord_digest: 'Daily collections digest',
  landlord_remittance_confirmed: 'Remittance confirmed',
}

function labelFor(key: string) {
  return TEMPLATE_LABELS[key.replace('.tmpl', '')] ?? key
}

const status = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
const templates = ref<SMSTemplate[]>([])
const drafts = ref<Record<string, string>>({})
const savingKey = ref<string | null>(null)
const saveErrors = ref<Record<string, string>>({})
const savedKey = ref<string | null>(null)

const tenantTemplates = computed(() => templates.value.filter((t) => t.actor === 'tenant'))
const landlordTemplates = computed(() => templates.value.filter((t) => t.actor === 'landlord'))

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  if (!permissions.hasRole('manager', 'landlord')) {
    await navigateTo(`/${route.params.slug}/properties`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: SMSTemplate[] }>('/v1/organization/sms-templates')
    templates.value = response.data
    drafts.value = Object.fromEntries(response.data.map((t) => [t.key, t.body]))
    status.value = 'ready'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load SMS templates.'
    status.value = 'error'
  }
}

function placeholderFor(name: string) {
  return '{{.' + name + '}}'
}

function insertVar(key: string, name: string) {
  drafts.value = { ...drafts.value, [key]: `${drafts.value[key] ?? ''}{{.${name}}}` }
}

async function save(key: string) {
  saveErrors.value = { ...saveErrors.value, [key]: '' }
  savedKey.value = null
  savingKey.value = key
  try {
    await api.request(`/v1/organization/sms-templates/${key}`, {
      method: 'PATCH',
      body: { body: drafts.value[key] },
    })
    const template = templates.value.find((t) => t.key === key)
    if (template) {
      template.body = drafts.value[key]
      template.override = true
    }
    savedKey.value = key
  } catch (e: any) {
    saveErrors.value = { ...saveErrors.value, [key]: e?.data?.error ?? 'Unable to save template.' }
  } finally {
    savingKey.value = null
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">SMS Templates</h1>
    </div>

    <p v-if="status === 'loading'" class="page-state">Loading templates&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>

    <template v-else>
      <section class="template-group">
        <h2 class="group-title">Tenant-facing</h2>
        <article v-for="t in tenantTemplates" :key="t.key" class="template-card">
          <div class="template-card-header">
            <h3>{{ labelFor(t.key) }}</h3>
            <span v-if="!t.override" class="badge">Default</span>
          </div>
          <textarea v-model="drafts[t.key]" rows="3"></textarea>
          <div class="vars-row">
            <span class="vars-label">Insert:</span>
            <button v-for="v in t.vars" :key="v" type="button" class="var-chip" @click="insertVar(t.key, v)">
              {{ placeholderFor(v) }}
            </button>
          </div>
          <p v-if="saveErrors[t.key]" class="page-error">{{ saveErrors[t.key] }}</p>
          <p v-else-if="savedKey === t.key" class="page-hint">Saved.</p>
          <button type="button" class="primary-button" :disabled="savingKey === t.key" @click="save(t.key)">
            {{ savingKey === t.key ? 'Saving...' : 'Save' }}
          </button>
        </article>
      </section>

      <section class="template-group">
        <h2 class="group-title">Landlord-facing</h2>
        <article v-for="t in landlordTemplates" :key="t.key" class="template-card">
          <div class="template-card-header">
            <h3>{{ labelFor(t.key) }}</h3>
            <span v-if="!t.override" class="badge">Default</span>
          </div>
          <textarea v-model="drafts[t.key]" rows="3"></textarea>
          <div class="vars-row">
            <span class="vars-label">Insert:</span>
            <button v-for="v in t.vars" :key="v" type="button" class="var-chip" @click="insertVar(t.key, v)">
              {{ placeholderFor(v) }}
            </button>
          </div>
          <p v-if="saveErrors[t.key]" class="page-error">{{ saveErrors[t.key] }}</p>
          <p v-else-if="savedKey === t.key" class="page-hint">Saved.</p>
          <button type="button" class="primary-button" :disabled="savingKey === t.key" @click="save(t.key)">
            {{ savingKey === t.key ? 'Saving...' : 'Save' }}
          </button>
        </article>
      </section>
    </template>
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
.page-hint { color: var(--color-text-muted, #6B7280); }
.page-state { color: var(--color-text-muted, #6B7280); }

.template-group { margin-bottom: var(--space-6); }

.group-title {
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
  margin-bottom: var(--space-3);
}

.template-card {
  display: grid;
  gap: var(--space-2);
  padding: var(--space-4);
  margin-bottom: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.template-card-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.template-card-header h3 { margin: 0; }

.badge {
  font-size: 0.75rem;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--surface-canvas, #F3F4F6);
  color: var(--color-text-muted, #6B7280);
}

textarea {
  width: 100%;
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  font-family: inherit;
  resize: vertical;
}

.vars-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.vars-label { color: var(--color-text-muted, #6B7280); font-size: 0.875rem; }

.var-chip {
  padding: 2px 8px;
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
  background: var(--surface-canvas, #F3F4F6);
  font-size: 0.8125rem;
  cursor: pointer;
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
</style>
