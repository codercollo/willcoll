<script setup lang="ts">
import type { Manager } from '~/stores/auth'

definePageMeta({ title: 'Account' })

const api = useApi()
const auth = useAuthStore()
const toast = useToast()

// Profile
const profile = useSubmit()
const firm = ref(auth.manager?.firm_name ?? '')
const phone = ref(auth.manager?.phone ?? '')
const dirty = computed(() => firm.value !== auth.manager?.firm_name || phone.value !== auth.manager?.phone)

onMounted(async () => {
  try {
    const res = await api.get<{ manager: Manager }>('/account')
    auth.setManager(res.manager)
    firm.value = res.manager.firm_name
    phone.value = res.manager.phone
  } catch (e) { toast.fail(e) }
})

async function saveProfile() {
  const res = await profile.run(() => api.patch<{ manager: Manager }>('/account', { firm_name: firm.value.trim(), phone: phone.value.trim() }))
  if (res) { auth.setManager(res.manager); toast.success('Profile saved') }
}

async function signOut() {
  await auth.logout()
  await navigateTo('/auth/login')
}

// Password
const pw = useSubmit()
const current = ref('')
const next = ref('')
const confirm = ref('')
const mismatch = computed(() => confirm.value !== '' && confirm.value !== next.value)

async function changePassword() {
  if (mismatch.value) return
  const ok = await pw.run(async () => { await api.put('/account/password', { current_password: current.value, new_password: next.value }); return true })
  if (ok) {
    current.value = next.value = confirm.value = ''
    toast.success('Password changed. Other devices were signed out.')
  }
}
</script>

<template>
  <div class="grid max-w-3xl gap-8">
    <UiCard>
      <h2 class="mb-1">Firm profile</h2>
      <p class="mb-6 text-gray-500">Your email and username are your sign-in identity and cannot be changed here.</p>
      <form class="flex max-w-md flex-col gap-5" novalidate @submit.prevent="saveProfile">
        <UiFormAlert v-if="profile.message.value">{{ profile.message.value }}</UiFormAlert>
        <UiField label="Email" for="email"><UiInput id="email" :model-value="auth.manager?.email ?? ''" disabled /></UiField>
        <UiField label="Username" for="username"><UiInput id="username" :model-value="auth.manager?.username ?? ''" disabled /></UiField>
        <UiField label="Firm name" for="firm" :error="profile.fields.value.firm_name"><UiInput id="firm" v-model="firm" :invalid="!!profile.fields.value.firm_name" /></UiField>
        <UiField label="Phone" for="phone" help="Kenyan number in +254 format" :error="profile.fields.value.phone"><UiInput id="phone" v-model="phone" type="tel" :invalid="!!profile.fields.value.phone" /></UiField>
        <div><UiButton type="submit" :loading="profile.loading.value" :disabled="!dirty">Save changes</UiButton></div>
      </form>
    </UiCard>

    <UiCard>
      <h2 class="mb-1">Change password</h2>
      <p class="mb-6 text-gray-500">Other devices are signed out when you change it. This one stays signed in.</p>
      <form class="flex max-w-md flex-col gap-5" novalidate @submit.prevent="changePassword">
        <UiFormAlert v-if="pw.message.value">{{ pw.message.value }}</UiFormAlert>
        <UiField label="Current password" for="current" :error="pw.fields.value.current_password"><UiInput id="current" v-model="current" type="password" autocomplete="current-password" :invalid="!!pw.fields.value.current_password" /></UiField>
        <UiField label="New password" for="new" help="8 to 72 characters." :error="pw.fields.value.new_password"><UiInput id="new" v-model="next" type="password" autocomplete="new-password" :invalid="!!pw.fields.value.new_password" /></UiField>
        <UiField label="Confirm new password" for="confirm" :error="mismatch ? 'Passwords do not match' : undefined"><UiInput id="confirm" v-model="confirm" type="password" autocomplete="new-password" :invalid="mismatch" /></UiField>
        <div><UiButton type="submit" :loading="pw.loading.value" :disabled="!current || !next || mismatch">Change password</UiButton></div>
      </form>
    </UiCard>

    <UiCard>
      <h2 class="mb-4">Session</h2>
      <UiButton variant="secondary" @click="signOut"><Icon name="lucide:log-out" class="size-4" />Sign out</UiButton>
    </UiCard>
  </div>
</template>
