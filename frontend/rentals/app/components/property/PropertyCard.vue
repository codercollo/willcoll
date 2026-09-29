<script setup lang="ts">
import type { Property } from '~/types/api'

const props = defineProps<{ property: Property }>()
const cur = useCurrency()
const s = computed(() => props.property.summary)
const total = computed(() => (s.value?.units_occupied ?? 0) + (s.value?.units_vacant ?? 0))

// Design-tokens 7.1: the bar turns red if under 60% collected past the 20th
// of the current month.
const ratio = computed(() => moneyRatio(s.value?.rent_collected, s.value?.rent_expected))
const behind = computed(() => {
  if (!s.value || s.value.period !== currentPeriod()) return false
  return nairobiParts(new Date()).day > 20 && ratio.value < 0.6
})
</script>

<template>
  <UiCard :to="`/properties/${property.id}`" class="flex flex-col">
    <div>
      <h3>{{ property.name }}</h3>
      <p class="mt-1 text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)] text-gray-500">{{ s?.landlord_name }} &middot; {{ property.location }}</p>
    </div>

    <div v-if="s" class="mt-4 flex flex-col gap-4">
      <div>
        <div class="flex items-baseline justify-between gap-2">
          <p class="text-[length:var(--text-caption)] text-gray-500">Rent collected vs expected</p>
          <p class="money text-[length:var(--text-money-sm)] font-semibold">{{ cur.format(s.rent_collected) }} <span class="font-normal text-gray-500">/ {{ cur.format(s.rent_expected) }}</span></p>
        </div>
        <div class="mt-2 h-1 rounded-full bg-gray-100" role="progressbar" :aria-valuenow="Math.round(ratio * 100)" aria-valuemin="0" aria-valuemax="100" aria-label="Rent collected">
          <div :class="['h-1 rounded-full transition-[width] duration-(--duration-base)', behind ? 'bg-arrears' : 'bg-accent-primary']" :style="{ width: `${Math.round(ratio * 100)}%` }" />
        </div>
      </div>
      <p class="flex items-center gap-3">
        <span>{{ s.units_occupied }} occupied / {{ total }} total</span>
        <span class="flex items-center gap-1 text-gray-500" aria-hidden="true">
          <span class="size-2 rounded-full bg-paid" /><span class="size-2 rounded-full bg-vacant" />
        </span>
      </p>
    </div>

    <div class="mt-4 flex justify-end border-t border-border-subtle pt-4">
      <span class="font-semibold text-accent-text">View property</span>
    </div>
  </UiCard>
</template>
