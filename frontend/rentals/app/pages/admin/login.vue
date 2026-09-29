<script setup lang="ts">
definePageMeta({ layout: 'public' })

const auth = useAuthStore()
const { loading, fields, message, run } = useSubmit()
const email = ref('')
const password = ref('')

async function submit() {
  const ok = await run(async () => { await auth.adminLogin(email.value.trim(), password.value); return true })
  if (ok) await navigateTo('/admin/managers')
}
</script>

<template>
  <UiAuthCard title="Admin sign in" subtitle="Willcoll platform administration.">
    <form class="flex flex-col gap-5" novalidate @submit.prevent="submit">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Email" for="email" :error="fields.email"><UiInput id="email" v-model="email" type="email" autocomplete="username" inputmode="email" :invalid="!!fields.email" /></UiField>
      <UiField label="Password" for="password" :error="fields.password"><UiInput id="password" v-model="password" type="password" autocomplete="current-password" :invalid="!!fields.password" /></UiField>
      <UiButton type="submit" block large :loading="loading" :disabled="!email || !password">Sign in</UiButton>
    </form>
  </UiAuthCard>
</template>
