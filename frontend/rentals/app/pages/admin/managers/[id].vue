<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { AdminManager } from '~/types/admin'

definePageMeta({ title: 'Manager', layout: 'admin' })

const route = useRoute()
const admin = useAdmin()
const toast = useToast()
const m = ref<AdminManager | null>(null)
const loading = ref(true)
const notFound = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    m.value = await admin.manager(String(route.params.id))
    route.meta.title = m.value.firm_name
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) notFound.value = true
    else error.value = e instanceof Error ? e.message : 'Could not load the manager.'
  } finally { loading.value = false }
}
onMounted(load)

// Suspending signs the firm out everywhere at once; reinstating lets them back in.
const confirm = ref(false)
const act = useSubmit()
const suspending = computed(() => m.value?.status !== 'suspended')
async function toggle() {
  if (!m.value) return
  const ok = await act.run(async () => { await (suspending.value ? admin.suspend(m.value!.id) : admin.reinstate(m.value!.id)); return true })
  if (ok) {
    toast.success(suspending.value ? `${m.value.firm_name} suspended and signed out.` : `${m.value.firm_name} reinstated.`)
    confirm.value = false
    await load()
  }
}
const confirmText = computed(() => (suspending.value
  ? `${m.value?.firm_name} is signed out everywhere immediately and cannot sign in until you reinstate it. Their data is kept.`
  : `${m.value?.firm_name} will be able to sign in again.`))
</script>

<template>
  <div class="flex flex-col gap-6">
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiCard v-if="notFound" :padded="false"><UiEmptyState title="Manager not found" icon="lucide:users"><UiButton to="/admin/managers">Back to managers</UiButton></UiEmptyState></UiCard>
    <UiSkeleton v-else-if="loading && !m" block />

    <template v-else-if="m">
      <NuxtLink to="/admin/managers" class="inline-flex items-center gap-1 text-gray-500 hover:text-gray-900"><Icon name="lucide:chevron-left" class="size-4" />Managers</NuxtLink>

      <UiCard>
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h2>{{ m.firm_name }}</h2>
            <p class="text-gray-500">{{ m.username }} &middot; {{ m.email }} &middot; <span class="tnum">{{ m.phone }}</span></p>
            <p class="mt-2"><AdminStatusBadge kind="manager" :status="m.status" /></p>
          </div>
          <UiButton v-if="m.status !== 'pending'" :variant="suspending ? 'destructive' : 'primary'" @click="confirm = true">
            <Icon :name="suspending ? 'lucide:ban' : 'lucide:rotate-ccw'" class="size-4" />{{ suspending ? 'Suspend firm' : 'Reinstate firm' }}
          </UiButton>
        </div>
        <UiFormAlert v-if="m.status === 'pending'" tone="info" class="mt-4">This firm has not activated its account yet.</UiFormAlert>
        <dl class="mt-6 grid grid-cols-2 gap-6 md:grid-cols-4">
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Subscription</dt><dd class="mt-1"><AdminStatusBadge kind="subscription" :status="m.subscription?.status" /></dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Paid until</dt><dd class="font-semibold">{{ m.subscription?.current_period_end ? formatDate(m.subscription.current_period_end) : '—' }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Properties</dt><dd class="tnum font-semibold">{{ m.property_count ?? 0 }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Units</dt><dd class="tnum font-semibold">{{ m.unit_count ?? 0 }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Joined</dt><dd class="font-semibold">{{ formatDate(m.created_at) }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Activated</dt><dd class="font-semibold">{{ m.activated_at ? formatDate(m.activated_at) : '—' }}</dd></div>
        </dl>
      </UiCard>

      <UiFormAlert tone="info">For privacy, the platform admin cannot see a firm's ledgers, tenants or payments. That is enforced in the database.</UiFormAlert>

      <UiConfirmDialog v-model="confirm" :title="suspending ? 'Suspend this firm?' : 'Reinstate this firm?'" :confirm-label="suspending ? 'Suspend' : 'Reinstate'" :destructive="suspending" :loading="act.loading.value" :text="confirmText" @confirm="toggle">
        <UiFormAlert v-if="act.message.value" class="mt-4">{{ act.message.value }}</UiFormAlert>
      </UiConfirmDialog>
    </template>
  </div>
</template>
