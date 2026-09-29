<script setup lang="ts">
// Month stepper for the billing tabs. Rent may be run up to next month, so
// that is the ceiling.
const model = defineModel<string>({ required: true })
const max = computed(() => shiftPeriod(currentPeriod(), 1))
</script>

<template>
  <div class="inline-flex items-center gap-1 rounded-sm border border-border-default bg-white">
    <UiButton variant="icon" label="Previous month" @click="model = shiftPeriod(model, -1)"><Icon name="lucide:chevron-left" class="size-5" /></UiButton>
    <span class="min-w-36 px-2 text-center font-semibold" aria-live="polite">{{ periodLabel(model) }}</span>
    <UiButton variant="icon" label="Next month" :disabled="model >= max" @click="model = shiftPeriod(model, 1)"><Icon name="lucide:chevron-right" class="size-5" /></UiButton>
  </div>
</template>
