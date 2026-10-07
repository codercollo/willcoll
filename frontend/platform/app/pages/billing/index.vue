<script setup lang="ts">
import { PRODUCTS } from '#base/utils/appLinks'

// "/billing": billing is per product, so pick one. The firm's own live
// product when there is one (a lone product goes straight to its page),
// otherwise a list of the available ones.
definePageMeta({ title: 'Billing' })

const auth = useAuthStore()
const loading = ref(true)

const live = computed(() => PRODUCTS.filter(p => p.available && auth.modules?.some(m => m.product === p.key && ['trialing', 'active'].includes(m.status))))

onMounted(async () => {
  await auth.fetchModules()
  loading.value = false
  if (live.value.length === 1) await navigateTo(`/billing/${live.value[0]!.key}`, { replace: true })
})
</script>

<template>
  <div class="grid max-w-2xl gap-6">
    <UiSkeleton v-if="loading" block />
    <UiCard v-else>
      <h2 class="mb-1">Choose a product</h2>
      <p class="mb-6 text-gray-500">Each product has its own plan and invoices.</p>
      <ul class="grid gap-3">
        <li v-for="p in PRODUCTS.filter(x => x.available)" :key="p.key">
          <UiButton variant="secondary" block :to="`/billing/${p.key}`"><Icon :name="p.icon" class="size-5" />{{ p.label }} billing</UiButton>
        </li>
      </ul>
    </UiCard>
  </div>
</template>
