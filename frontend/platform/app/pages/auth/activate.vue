<script setup lang="ts">
definePageMeta({ layout: 'public' })

const api = useApi()
const route = useRoute()
const token = computed(() => String(route.query.token ?? ''))
const state = ref<'working' | 'ok' | 'failed' | 'idle'>(token.value ? 'working' : 'idle')
const error = ref('')

// Resend form (also the landing when there is no token in the link).
const { loading, fields, message, run } = useSubmit()
const email = ref('')
const sent = ref(false)

onMounted(async () => {
  if (!token.value) return
  try {
    await api.put('/managers/activated', { token: token.value })
    state.value = 'ok'
  } catch (e) {
    state.value = 'failed'
    error.value = e instanceof Error ? e.message : 'The link is invalid or has expired.'
  }
})

async function resend() {
  const ok = await run(async () => { await api.post('/tokens/activation', { email: email.value.trim() }); return true })
  if (ok) sent.value = true
}
</script>

<template>
  <UiAuthCard v-if="state === 'working'" title="Activating your account">
    <UiSkeleton :lines="2" />
  </UiAuthCard>

  <UiAuthCard v-else-if="state === 'ok'" title="Account activated" subtitle="Your 7-day free trial has started. Sign in to begin.">
    <UiButton block large to="/auth/login">Continue to sign in</UiButton>
  </UiAuthCard>

  <UiAuthCard v-else :title="state === 'failed' ? 'That link did not work' : 'Resend activation email'" subtitle="Enter your email and we will send a fresh link.">
    <UiFormAlert v-if="state === 'failed'" class="mb-5">{{ error }}</UiFormAlert>
    <UiFormAlert v-if="sent" tone="success">If that email has an account waiting to be activated, a new link is on its way.</UiFormAlert>
    <form v-else class="flex flex-col gap-5" novalidate @submit.prevent="resend">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Email" for="email" :error="fields.email"><UiInput id="email" v-model="email" type="email" inputmode="email" :invalid="!!fields.email" /></UiField>
      <UiButton type="submit" block large :loading="loading" :disabled="!email">Send activation email</UiButton>
    </form>
    <template #footer><NuxtLink to="/auth/login" class="font-semibold text-accent-text">Back to sign in</NuxtLink></template>
  </UiAuthCard>
</template>
