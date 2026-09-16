<script setup lang="ts">
definePageMeta({ layout: false })

const branding = useBranding()
const auth = useAuth()

const email = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  loading.value = true
  try {
    await auth.login(email.value, password.value)
    await navigateTo('/')
  } catch (e: any) {
    error.value = e?.data?.error ?? 'Unable to sign in. Check your credentials and try again.'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="login-shell">
    <section class="login-card">
      <img
        v-if="branding.logo_url"
        :src="branding.logo_url"
        :alt="branding.brand_name"
        class="login-logo"
      >
      <div v-else class="login-logo-placeholder" aria-hidden="true" />

      <h1 class="login-title">{{ branding.brand_name }}</h1>
      <p class="login-subtitle">Sign in to your property dashboard.</p>

      <form class="login-form" @submit.prevent="submit">
        <label class="field">
          <span>Email</span>
          <input v-model="email" type="email" autocomplete="username" placeholder="you@example.com" required>
        </label>
        <label class="field">
          <span>Password</span>
          <input v-model="password" type="password" autocomplete="current-password" placeholder="Your password" required>
        </label>
        <p v-if="error" class="form-error">{{ error }}</p>
        <button type="submit" class="primary-button" :disabled="loading">
          {{ loading ? 'Signing in...' : 'Sign in' }}
        </button>
      </form>
    </section>
  </main>
</template>

<style scoped>
.login-shell {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: var(--space-4);
  background: var(--surface-flat, #F5F6F8);
}

.login-card {
  width: min(100%, 400px);
  padding: var(--space-12);
  background: var(--surface-card, #FFFFFF);
  border-radius: var(--layout-card-radius, 10px);
  box-shadow: var(--shadow-card);
  text-align: center;
}

.login-logo {
  max-height: 48px;
  margin-bottom: var(--space-4);
}

.login-logo-placeholder {
  width: 48px;
  height: 48px;
  margin: 0 auto var(--space-4);
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
}

.login-title {
  margin: 0;
  font-family: var(--font-display, serif);
  font-size: var(--text-display-md, 1.563rem);
  color: var(--color-text-primary, #1F2320);
}

.login-subtitle {
  margin: var(--space-2) 0 var(--space-8);
  color: var(--color-text-muted, #6B7280);
}

.login-form {
  display: grid;
  gap: var(--space-4);
  text-align: left;
}

.field {
  display: grid;
  gap: var(--space-1);
  color: var(--color-text-primary, #1F2320);
}

.field input {
  padding: var(--space-3);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.form-error {
  color: var(--color-status-error, #C6362E);
}

.primary-button {
  padding: var(--space-3);
  border: 0;
  border-radius: var(--radius-md, 6px);
  background: var(--color-action-primary, #E8702A);
  color: #FFFFFF;
  font-weight: 600;
  cursor: pointer;
}

.primary-button:disabled {
  opacity: 0.7;
  cursor: default;
}
</style>
