<script setup lang="ts">
import { PRODUCTS, appUrl } from '#base/utils/appLinks'
import { moduleView } from '../utils/modules'

// "/": a signed-in firm's products as cards (the guard sends signed-out visitors
// to /auth/login and a Super Admin to /admin/managers). A card opens that
// product's app; a product the firm has not started offers its owner "Start
// free trial" on the Modules page. Staff only get the products in their
// module_access (platform-api filters GET /modules the same way).
definePageMeta({ title: 'Your products' })

const auth = useAuthStore()
const links = useAppLinks()
const loading = ref(true)

onMounted(async () => {
  await auth.fetchModules(true)
  loading.value = false
})

const cards = computed(() => PRODUCTS
  .map(p => ({ ...p, view: moduleView(auth.modules?.find(m => m.product === p.key)), url: appUrl(links.apps, p.key) }))
  // Staff never see products they cannot use; owners see every product.
  .filter(c => auth.isOwner || c.view.state !== 'none'))

const canOpen = (c: (typeof cards.value)[number]) => !!c.url && ['trial', 'active'].includes(c.view.state)
</script>

<template>
  <div class="grid gap-6">
    <UiSkeleton v-if="loading" block />
    <UiEmptyState v-else-if="!cards.length" title="No products yet" text="Ask the account owner to give you access to a product." icon="lucide:layout-grid" />
    <div v-else class="grid grid-cols-1 gap-6 md:grid-cols-2 xl:grid-cols-3">
      <UiCard v-for="c in cards" :key="c.key" :data-testid="`product-${c.key}`">
        <div class="flex items-start justify-between gap-3">
          <span class="flex size-11 items-center justify-center rounded-sm bg-accent-primary-tint text-accent-text"><Icon :name="c.icon" class="size-6" /></span>
          <UiBadge v-if="c.available" :status="c.view.badge" :label="c.view.label" />
          <UiBadge v-else status="vacant" label="Coming soon" />
        </div>
        <h2 class="mt-4">{{ c.label }}</h2>
        <p class="mt-1 mb-6 text-gray-500">{{ c.blurb }}</p>
        <UiButton v-if="canOpen(c)" :to="c.url">Open {{ c.label }}<Icon name="lucide:arrow-up-right" class="size-4" /></UiButton>
        <UiButton v-else-if="c.available && auth.isOwner" variant="secondary" to="/modules">{{ c.view.state === 'none' ? 'Start free trial' : 'Manage' }}</UiButton>
        <p v-else-if="!c.available" class="text-gray-500">We will let you know when it is ready.</p>
      </UiCard>
    </div>
  </div>
</template>
