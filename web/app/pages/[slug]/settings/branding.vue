<script setup lang="ts">
definePageMeta({ layout: 'default' })

const auth = useAuth()
const api = useApi()
const route = useRoute()
const permissions = usePermissions()

// Closed set, spec §1b.3 — never a freeform color input.
const ACCENT_KEYS = [
  { key: 'orange', label: 'Orange', hex: '#E8702A' },
  { key: 'teal', label: 'Teal', hex: '#1B8A7A' },
  { key: 'indigo', label: 'Indigo', hex: '#4A55B8' },
  { key: 'crimson', label: 'Crimson', hex: '#C13B4B' },
  { key: 'forest', label: 'Forest', hex: '#2F7D4B' },
]

interface Organization {
  name: string
  slug: string
  brand_name: string
  logo_url: string | null
  accent_key: string
  sms_sender_id: string | null
  email_from_name: string | null
}

const status = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
const org = ref<Organization | null>(null)

const form = ref({ brand_name: '', accent_key: 'orange', sms_sender_id: '', email_from_name: '' })
const logoFile = ref<File | null>(null)
const saving = ref(false)
const saveError = ref('')
const saved = ref(false)

async function load() {
  if (!auth.token.value) {
    await navigateTo(`/${route.params.slug}/login`)
    return
  }
  if (!permissions.isSuperManager.value) {
    await navigateTo(`/${route.params.slug}/properties`)
    return
  }
  status.value = 'loading'
  try {
    const response = await api.request<{ data: Organization }>('/v1/organization')
    org.value = response.data
    form.value = {
      brand_name: response.data.brand_name,
      accent_key: response.data.accent_key,
      sms_sender_id: response.data.sms_sender_id ?? '',
      email_from_name: response.data.email_from_name ?? '',
    }
    status.value = 'ready'
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to load organization branding.'
    status.value = 'error'
  }
}

function onLogoChange(event: Event) {
  logoFile.value = (event.target as HTMLInputElement).files?.[0] ?? null
}

async function save() {
  saveError.value = ''
  saved.value = false
  saving.value = true
  try {
    const body = new FormData()
    body.set('brand_name', form.value.brand_name)
    body.set('accent_key', form.value.accent_key)
    body.set('sms_sender_id', form.value.sms_sender_id)
    body.set('email_from_name', form.value.email_from_name)
    if (logoFile.value) body.set('logo', logoFile.value)

    const response = await api.request<{ data: Organization }>('/v1/organization/branding', {
      method: 'PATCH',
      body,
    })
    org.value = response.data
    logoFile.value = null
    saved.value = true
  } catch (e: any) {
    saveError.value = e?.data?.error ?? 'Unable to save branding.'
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <h1 class="page-title">Branding</h1>
    </div>

    <p v-if="status === 'loading'" class="page-state">Loading branding&hellip;</p>
    <p v-else-if="status === 'error'" class="page-state page-error">{{ error }}</p>

    <RoleGate v-else :roles="['manager']">
      <form class="branding-form" @submit.prevent="save">
        <label class="field">
          <span>Brand name</span>
          <input v-model="form.brand_name" type="text" required>
        </label>

        <fieldset class="field accent-field">
          <legend>Accent color</legend>
          <div class="accent-grid">
            <label v-for="accent in ACCENT_KEYS" :key="accent.key" class="accent-option">
              <input v-model="form.accent_key" type="radio" name="accent_key" :value="accent.key">
              <span class="swatch" :style="{ background: accent.hex }"></span>
              {{ accent.label }}
            </label>
          </div>
        </fieldset>

        <label class="field">
          <span>Logo</span>
          <img v-if="org?.logo_url" :src="org.logo_url" alt="Current logo" class="logo-preview">
          <input type="file" accept="image/*" @change="onLogoChange">
        </label>

        <label class="field">
          <span>SMS sender ID</span>
          <input v-model="form.sms_sender_id" type="text">
        </label>

        <label class="field">
          <span>Email from name</span>
          <input v-model="form.email_from_name" type="text">
        </label>

        <p v-if="saveError" class="page-error">{{ saveError }}</p>
        <p v-if="saved" class="page-hint">Branding saved.</p>
        <button type="submit" class="primary-button" :disabled="saving">
          {{ saving ? 'Saving...' : 'Save branding' }}
        </button>
      </form>
    </RoleGate>
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

.branding-form {
  display: grid;
  gap: var(--space-4);
  max-width: 480px;
  padding: var(--space-4);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
}

.field { display: grid; gap: var(--space-1); border: 0; padding: 0; margin: 0; }
.field input[type="text"] {
  padding: var(--space-2);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.accent-field legend { padding: 0; font-weight: 600; }
.accent-grid {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.accent-option {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  cursor: pointer;
}

.swatch {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: inline-block;
  border: var(--border-hairline, 1px solid #E2E5E9);
}

.logo-preview {
  max-height: 48px;
  margin-bottom: var(--space-2);
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
