<script setup lang="ts">
definePageMeta({ layout: 'public' })

// A staff member's invite link (PUT /staff/activated): they set their own
// password, which activates the account, then sign in as usual.
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
  const ok = await run(async () => { await api.put('/staff/activated', { token: token.value, password: password.value }); return true })
  if (ok) done.value = true
}
</script>

<template>
  <UiAuthCard v-if="!token" title="Invite link missing" subtitle="Open the link from your invitation email. Ask the account owner to invite you again if it has expired.">
    <UiButton block large to="/auth/login">Back to sign in</UiButton>
  </UiAuthCard>

  <UiAuthCard v-else-if="done" title="You are all set" subtitle="Your account is active. Sign in with your email and the password you just chose.">
    <UiButton block large to="/auth/login">Sign in</UiButton>
  </UiAuthCard>

  <UiAuthCard v-else title="Accept your invite" subtitle="Choose a password to join your team on Willcoll.">
    <form class="flex flex-col gap-5" novalidate @submit.prevent="submit">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiFormAlert v-if="fields.token">{{ fields.token }}. Ask the account owner to send a new invite.</UiFormAlert>
      <UiField label="Password" for="password" help="8 to 72 characters." :error="fields.password"><UiInput id="password" v-model="password" type="password" autocomplete="new-password" :invalid="!!fields.password" /></UiField>
      <UiField label="Confirm password" for="confirm" :error="mismatch ? 'Passwords do not match' : undefined"><UiInput id="confirm" v-model="confirm" type="password" autocomplete="new-password" :invalid="mismatch" /></UiField>
      <UiButton type="submit" block large :loading="loading" :disabled="!password || mismatch">Activate my account</UiButton>
    </form>
  </UiAuthCard>
</template>
