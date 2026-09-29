<script setup lang="ts">
import type { BadgeStatus } from '~/components/ui/Badge.vue'

// One place that maps a firm or subscription status to a badge (never colour alone).
const props = defineProps<{ kind: 'manager' | 'subscription'; status?: string | null }>()

const map: Record<string, Record<string, { status: BadgeStatus; label: string }>> = {
  manager: { active: { status: 'paid', label: 'Active' }, pending: { status: 'pending', label: 'Pending' }, suspended: { status: 'arrears', label: 'Suspended' } },
  subscription: { active: { status: 'paid', label: 'Active' }, trialing: { status: 'info', label: 'Trial' }, past_due: { status: 'arrears', label: 'Past due' }, cancelled: { status: 'vacant', label: 'Cancelled' } },
}
const badge = computed(() => (props.status ? map[props.kind]?.[props.status] : undefined))
</script>

<template>
  <UiBadge v-if="badge" :status="badge.status" :label="badge.label" />
  <UiBadge v-else status="vacant" label="No plan" />
</template>
