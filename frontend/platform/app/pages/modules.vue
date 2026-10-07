<script setup lang="ts">
import { PRODUCTS, appUrl } from '#base/utils/appLinks'
import { moduleView } from '../utils/modules'

// Modules (owner only): each product with its standing and trial days left.
// A product the firm has not started can have its free trial started here
// (POST /modules/:product/trial, once; a repeat never extends it). Products
// that are not built yet show "Coming soon": there is nothing to activate.
definePageMeta({ title: 'Modules' })

const api = useApi()
const auth = useAuthStore()
const toast = useToast()
const links = useAppLinks()
const loading = ref(true)
const starting = ref('')

async function load() {
  loading.value = true
  try {
    await auth.fetchModules(true)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const rows = computed(() => PRODUCTS.map(p => ({ ...p, view: moduleView(auth.modules?.find(m => m.product === p.key)), url: appUrl(links.apps, p.key) })))

async function startTrial(product: string, label: string) {
  starting.value = product
  try {
    await api.post(`/modules/${product}/trial`)
    toast.success(`${label} trial started.`)
    await auth.fetchModules(true)
  } catch (e) {
    toast.fail(e)
  } finally {
    starting.value = ''
  }
}
</script>

<template>
  <div class="grid max-w-4xl gap-6">
    <UiSkeleton v-if="loading" block />
    <UiCard v-else :padded="false">
      <ul>
        <li v-for="r in rows" :key="r.key" class="flex flex-wrap items-center gap-4 border-b border-border-subtle px-6 py-5 last:border-b-0" :data-testid="`module-${r.key}`">
          <span class="flex size-11 shrink-0 items-center justify-center rounded-sm bg-accent-primary-tint text-accent-text"><Icon :name="r.icon" class="size-6" /></span>
          <div class="min-w-0 flex-1">
            <p class="font-semibold">{{ r.label }}</p>
            <p class="text-gray-500">{{ r.blurb }}</p>
          </div>
          <UiBadge v-if="r.available" :status="r.view.badge" :label="r.view.label" />
          <UiBadge v-else status="vacant" label="Coming soon" />
          <div class="flex w-full justify-end gap-2 sm:w-auto">
            <UiButton v-if="r.available && r.view.state === 'none'" :loading="starting === r.key" @click="startTrial(r.key, r.label)">Start free trial</UiButton>
            <UiButton v-if="r.available && ['trial', 'trial_ended', 'active'].includes(r.view.state)" variant="secondary" :to="`/billing/${r.key}`">Billing</UiButton>
            <UiButton v-if="r.url && ['trial', 'active'].includes(r.view.state)" :to="r.url">Open</UiButton>
          </div>
        </li>
      </ul>
    </UiCard>
    <p class="text-gray-500">Estates and Waste are on the way. Your free trial for each starts when you switch it on.</p>
  </div>
</template>
