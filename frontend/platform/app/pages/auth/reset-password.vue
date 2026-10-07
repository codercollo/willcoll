<script setup lang="ts">
definePageMeta({ layout: 'public' })

const api = useApi()
const route = useRoute()
const token = computed(() => String(route.query.token ?? ''))
const { loading, fields, message, run } = useSubmit()
const password = ref('')
const confirm = ref('')
const done = ref(false)
const mismatch = computed(() => confirm.value !== '' && confirm.value !== password.value)

async function submit() {
  if (mismatch.value) return
  const ok = await run(async () => { await api.put('/managers/password', { token: token.value, password: password.value }); return true })
  if (ok) done.value = true
}
</script>

<template>
  <UiAuthCard v-if="!token" title="Reset link missing" subtitle="Open the link from your email, or request a new one.">
    <UiButton block large to="/auth/forgot-password">Request a reset link</UiButton>
  </UiAuthCard>

  <UiAuthCard v-else-if="done" title="Password changed" subtitle="You have been signed out everywhere. Sign in with your new password.">
    <UiButton block large to="/auth/login">Sign in</UiButton>
  </UiAuthCard>

  <UiAuthCard v-else title="Choose a new password">
    <form class="flex flex-col gap-5" novalidate @submit.prevent="submit">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiFormAlert v-if="fields.token">{{ fields.token }} <NuxtLink to="/auth/forgot-password" class="font-semibold text-accent-text">Request a new link</NuxtLink></UiFormAlert>
      <UiField label="New password" for="password" help="8 to 72 characters." :error="fields.password"><UiInput id="password" v-model="password" type="password" autocomplete="new-password" :invalid="!!fields.password" /></UiField>
      <UiField label="Confirm password" for="confirm" :error="mismatch ? 'Passwords do not match' : undefined"><UiInput id="confirm" v-model="confirm" type="password" autocomplete="new-password" :invalid="mismatch" /></UiField>
      <UiButton type="submit" block large :loading="loading" :disabled="!password || mismatch">Change password</UiButton>
    </form>
  </UiAuthCard>
</template>
