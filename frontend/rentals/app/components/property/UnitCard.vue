<script setup lang="ts">
import type { Unit } from '~/types/api'

const props = defineProps<{ unit: Unit; propertyId: string; selectMode?: boolean; selected?: boolean }>()
const emit = defineEmits<{ toggle: [] }>()

// A vacant unit has no lease, so nothing to bind a QR code to.
const selectable = computed(() => props.selectMode && !!props.unit.current_lease)
</script>

<template>
  <UiCard
    compact
    :to="selectMode ? undefined : `/properties/${propertyId}/units/${unit.id}`"
    :class="['flex flex-col gap-1', selectMode && (selectable ? 'cursor-pointer' : 'opacity-50')]"
    @click="selectable && emit('toggle')"
  >
    <div class="flex items-start justify-between gap-2">
      <span class="flex items-center gap-2">
        <input v-if="selectMode" type="checkbox" class="size-4 accent-accent-primary" :checked="selected" :disabled="!selectable"
          :aria-label="`Select ${unit.unit_code}`" @click.stop @change="selectable && emit('toggle')">
        <h3 class="tnum">{{ unit.unit_code }}</h3>
      </span>
      <span class="flex items-center gap-2">
        <Icon v-if="unit.current_lease?.has_qr" name="lucide:qr-code" class="size-4 text-gray-500" title="Has a door sticker QR code" aria-label="Has a QR code" />
        <UiBadge :status="unit.status === 'vacant' ? 'vacant' : 'info'" :label="unit.status === 'vacant' ? 'Vacant' : 'Occupied'" />
      </span>
    </div>
    <p v-if="unit.current_lease" class="truncate">{{ unit.current_lease.tenant_name }}</p>
    <p v-else class="text-gray-500">No tenant</p>
    <p class="text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)] text-gray-500">
      <template v-if="unit.current_lease">Leased since {{ formatDate(unit.current_lease.start_date) }}</template>
      <template v-else-if="unit.meter_number">Meter {{ unit.meter_number }}</template>
      <template v-else>&nbsp;</template>
    </p>
  </UiCard>
</template>
