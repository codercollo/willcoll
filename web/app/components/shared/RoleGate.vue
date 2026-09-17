<script setup lang="ts">
import type { UserRole, PropertyGrant, PbacAction } from '~/composables/usePermissions'

// Wraps anything role/PBAC-gated (spec Phase 2.4) — hides content the actor
// can't legally use, instead of showing it disabled. roles is a base RBAC
// role check; action is a base-capability check (spec §2.1); pbacAction+grant
// additionally narrows an Agent to an actual per-property grant (a Manager/
// Landlord always passes once roles allows them). Any combination may be
// supplied; all supplied checks must pass.
const props = defineProps<{
  roles?: UserRole[]
  action?: string
  pbacAction?: PbacAction
  grant?: PropertyGrant | null
}>()

const permissions = usePermissions()

const allowed = computed(() => {
  if (props.roles && !permissions.hasRole(...props.roles)) return false
  if (props.action && !permissions.hasBaseAction(props.action)) return false
  if (props.pbacAction && !permissions.canOnProperty(props.pbacAction, props.grant)) return false
  return true
})
</script>

<template>
  <template v-if="allowed"><slot /></template>
</template>
