<script setup lang="ts">
definePageMeta({ layout: 'public' })

const api = useApi()
const { loading, fields, message, run } = useSubmit()
const email = ref('')
const sent = ref(false)

async function submit() {
  const ok = await run(async () => { await api.post('/tokens/password-reset', { email: email.value.trim() }); return true })
  if (ok) sent.value = true
}
</script>

<template>
  <UiAuthCard title="Forgot your password?" subtitle="Enter your email and we will send a reset link.">
    <UiFormAlert v-if="sent" tone="success">If that email has an account, a reset link is on its way. It expires soon, so use it promptly.</UiFormAlert>
    <form v-else class="flex flex-col gap-5" novalidate @submit.prevent="submit">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Email" for="email" :error="fields.email"><UiInput id="email" v-model="email" type="email" inputmode="email" :invalid="!!fields.email" /></UiField>
      <UiButton type="submit" block large :loading="loading" :disabled="!email">Send reset link</UiButton>
    </form>
    <template #footer><NuxtLink to="/auth/login" class="font-semibold text-accent-text">Back to sign in</NuxtLink></template>
  </UiAuthCard>
</template>
