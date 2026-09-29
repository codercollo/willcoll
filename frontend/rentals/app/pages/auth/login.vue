<script setup lang="ts">
definePageMeta({ layout: 'public' })

const auth = useAuthStore()
const { loading, fields, message, status, run } = useSubmit()
const email = ref('')
const password = ref('')

async function submit() {
  const ok = await run(async () => { await auth.login(email.value.trim(), password.value); return true })
  if (!ok) return
  // A brand-new firm lands on the setup guide; everyone else on their properties.
  const ob = await useOnboarding().fetchStatus().catch(() => null)
  await navigateTo(ob && ob.done === 0 ? '/onboarding' : '/properties')
}
</script>

<template>
  <UiAuthCard title="Sign in" subtitle="Welcome back to Willcoll.">
    <form class="flex flex-col gap-5" novalidate @submit.prevent="submit">
      <UiFormAlert v-if="message">
        {{ message }}
        <NuxtLink v-if="status === 403 && !/suspend/i.test(message)" to="/auth/activate" class="ml-1 font-semibold text-accent-text">Resend activation email</NuxtLink>
      </UiFormAlert>
      <UiField label="Email" for="email" :error="fields.email">
        <UiInput id="email" v-model="email" type="email" autocomplete="username" inputmode="email" :invalid="!!fields.email" />
      </UiField>
      <UiField label="Password" for="password" :error="fields.password">
        <UiInput id="password" v-model="password" type="password" autocomplete="current-password" :invalid="!!fields.password" />
      </UiField>
      <NuxtLink to="/auth/forgot-password" class="-mt-2 self-end text-accent-text">Forgot password?</NuxtLink>
      <UiButton type="submit" block large :loading="loading" :disabled="!email || !password">Sign in</UiButton>
    </form>
    <template #footer>New to Willcoll? <NuxtLink to="/auth/signup" class="font-semibold text-accent-text">Create an account</NuxtLink></template>
  </UiAuthCard>
</template>
