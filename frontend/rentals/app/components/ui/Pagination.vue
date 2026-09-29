<script setup lang="ts">
// Driven by the API's list metadata (features 1.5). Empty metadata hides it.
export interface PageMeta { current_page: number; page_size: number; first_page: number; last_page: number; total_records: number }
const props = defineProps<{ meta?: Partial<PageMeta> | null }>()
const emit = defineEmits<{ change: [page: number] }>()

const show = computed(() => !!props.meta?.last_page && props.meta.last_page > 1)
const page = computed(() => props.meta?.current_page ?? 1)
const last = computed(() => props.meta?.last_page ?? 1)
</script>

<template>
  <div v-if="show" class="flex items-center justify-between gap-4 px-5 py-3 text-gray-500">
    <p class="tnum">{{ meta?.total_records }} records</p>
    <div class="flex items-center gap-2">
      <UiButton variant="secondary" :disabled="page <= 1" @click="emit('change', page - 1)"><Icon name="lucide:chevron-left" class="size-4" />Prev</UiButton>
      <span class="tnum px-2">Page {{ page }} of {{ last }}</span>
      <UiButton variant="secondary" :disabled="page >= last" @click="emit('change', page + 1)">Next<Icon name="lucide:chevron-right" class="size-4" /></UiButton>
    </div>
  </div>
</template>
