<script setup lang="ts">
definePageMeta({ layout: 'public' })

const api = useApi()
const { loading, fields, message, run } = useSubmit()
const form = reactive({ firm_name: '', username: '', email: '', phone: '', password: '' })
const done = ref(false)

function normalisePhone(p: string) {
  const d = p.replace(/[\s-]/g, '')
  if (/^0[17]\d{8}$/.test(d)) return '+254' + d.slice(1)
  if (/^254\d{9}$/.test(d)) return '+' + d
  return d
}

async function submit() {
  const ok = await run(async () => {
    await api.post('/managers', { ...form, email: form.email.trim(), phone: normalisePhone(form.phone) })
    return true
  })
  if (ok) done.value = true
}
</script>

<template>
  <UiAuthCard v-if="!done" title="Create your account" subtitle="Try Willcoll free for 7 days. No card needed. Manage rent, water and garbage for every property in one place.">
    <form class="flex flex-col gap-5" novalidate @submit.prevent="submit">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Firm name" for="firm" :error="fields.firm_name"><UiInput id="firm" v-model="form.firm_name" autocomplete="organization" :invalid="!!fields.firm_name" /></UiField>
      <UiField label="Username" for="username" help="Used to sign in and in your pay-page links." :error="fields.username"><UiInput id="username" v-model="form.username" autocomplete="username" :invalid="!!fields.username" /></UiField>
      <UiField label="Email" for="email" :error="fields.email"><UiInput id="email" v-model="form.email" type="email" autocomplete="email" inputmode="email" :invalid="!!fields.email" /></UiField>
      <UiField label="Phone" for="phone" help="Kenyan number, e.g. 0722 000 000" :error="fields.phone"><UiInput id="phone" v-model="form.phone" type="tel" autocomplete="tel" inputmode="tel" :invalid="!!fields.phone" /></UiField>
      <UiField label="Password" for="password" help="8 to 72 characters." :error="fields.password"><UiInput id="password" v-model="form.password" type="password" autocomplete="new-password" :invalid="!!fields.password" /></UiField>
      <UiButton type="submit" block large :loading="loading">Create account</UiButton>
    </form>
    <template #footer>Already have an account? <NuxtLink to="/auth/login" class="font-semibold text-accent-text">Sign in</NuxtLink></template>
  </UiAuthCard>

  <UiAuthCard v-else title="Check your email" :subtitle="`We sent an activation link to ${form.email}.`">
    <UiFormAlert tone="success">Open the link in that email to activate your account, then sign in.</UiFormAlert>
    <template #footer>Did not get it? <NuxtLink to="/auth/activate" class="font-semibold text-accent-text">Resend the email</NuxtLink></template>
  </UiAuthCard>
</template>
