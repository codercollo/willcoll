<script setup lang="ts">
// A unit link without its property (the review queue only knows the unit id):
// look the unit up and continue to its full page.
definePageMeta({ title: 'Unit' })

const route = useRoute()
const units = useUnits()
const missing = ref(false)

onMounted(async () => {
  try {
    const u = await units.get(String(route.params.unitId))
    const tab = route.query.tab ? { tab: String(route.query.tab) } : undefined
    await navigateTo({ path: `/properties/${u.property_id}/units/${u.id}`, query: tab }, { replace: true })
  } catch { missing.value = true }
})
</script>

<template>
  <UiCard v-if="missing" :padded="false"><UiEmptyState title="Unit not found" icon="lucide:door-open"><UiButton to="/properties">Back to properties</UiButton></UiEmptyState></UiCard>
  <UiSkeleton v-else block />
</template>
