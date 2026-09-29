<script setup lang="ts">
export type BadgeStatus = 'paid' | 'arrears' | 'partial' | 'vacant' | 'pending' | 'review' | 'info'
const props = defineProps<{ status: BadgeStatus; label?: string }>()

// Never colour alone: every status has its own text (design-tokens 7.5).
const text: Record<BadgeStatus, string> = { paid: 'Paid', arrears: 'Arrears', partial: 'Partial', vacant: 'Vacant', pending: 'Pending', review: 'Review', info: 'Info' }
const tone: Record<BadgeStatus, string> = {
  paid: 'bg-paid-tint text-paid-text',
  arrears: 'bg-arrears-tint text-arrears-text',
  partial: 'bg-partial-tint text-partial-text',
  vacant: 'bg-vacant-tint text-vacant',
  pending: 'bg-pending-tint text-pending-text',
  review: 'bg-unmatched-tint text-unmatched-text',
  info: 'bg-accent-primary-tint text-accent-text',
}
</script>

<template>
  <span :class="['inline-flex h-[22px] items-center gap-1 rounded-full px-3 text-[length:var(--text-label)] font-semibold whitespace-nowrap', tone[props.status]]">
    <span class="size-[6px] rounded-full bg-current" aria-hidden="true" />
    {{ label ?? text[props.status] }}
  </span>
</template>
