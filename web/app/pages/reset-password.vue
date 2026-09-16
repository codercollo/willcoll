<script setup lang="ts">
definePageMeta({ layout: false })

const branding = useBranding()
const auth = useAuth()
const route = useRoute()

const token = typeof route.query.token === 'string' ? route.query.token : ''
const newPassword = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  loading.value = true
  try {
    await auth.resetPassword(token, newPassword.value)
    await navigateTo('/login')
  } catch {
    error.value = 'This reset link is invalid or expired. Ask your Manager to send a new one.'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="auth-shell">
    <section class="auth-card">
      <img v-if="branding.logo_url" :src="branding.logo_url" :alt="branding.brand_name" class="auth-logo">
      <div v-else class="auth-logo-placeholder" aria-hidden="true" />

      <h1 class="auth-title">{{ branding.brand_name }}</h1>
      <p class="auth-subtitle">Choose a new password.</p>

      <form class="auth-form" @submit.prevent="submit">
        <label class="field">
          <span>New password</span>
          <input v-model="newPassword" type="password" autocomplete="new-password" required>
        </label>
        <p v-if="error" class="form-error">{{ error }}</p>
        <button type="submit" class="primary-button" :disabled="loading || !token">
          {{ loading ? 'Updating...' : 'Update password' }}
        </button>
      </form>
    </section>
  </main>
</template>

<style scoped>
.auth-shell {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: var(--space-4);
  background: var(--surface-flat, #F5F6F8);
}

.auth-card {
  width: min(100%, 400px);
  padding: var(--space-12);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
  text-align: center;
}

.auth-logo { max-height: 48px; margin-bottom: var(--space-4); }
.auth-logo-placeholder {
  width: 48px; height: 48px; margin: 0 auto var(--space-4);
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
}

.auth-title {
  margin: 0;
  font-family: var(--font-display, serif);
  font-size: var(--text-display-md, 1.563rem);
  color: var(--color-text-primary, #1F2320);
}

.auth-subtitle { margin: var(--space-2) 0 var(--space-8); color: var(--color-text-muted, #6B7280); }

.auth-form { display: grid; gap: var(--space-4); text-align: left; }
.field { display: grid; gap: var(--space-1); color: var(--color-text-primary, #1F2320); }
.field input {
  padding: var(--space-3);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.form-error { color: var(--color-status-error, #C6362E); }

.primary-button {
  padding: var(--space-3);
  border: 0;
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
  color: #FFFFFF;
  font-weight: 600;
  cursor: pointer;
}

.primary-button:disabled { opacity: 0.7; cursor: default; }
</style>
