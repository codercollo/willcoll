<script setup lang="ts">
import type { NavItem } from '~/components/ui/AppShell.vue'

const auth = useAuthStore()
const nav: NavItem[] = [
  { label: 'Managers', to: '/admin/managers', icon: 'lucide:users' },
  { label: 'Subscriptions', to: '/admin/subscriptions', icon: 'lucide:receipt' },
  { label: 'System', to: '/admin/system', icon: 'lucide:activity' },
]
</script>

<template>
  <UiAppShell brand="Admin" :nav="nav" :user-name="auth.admin?.email" user-sub="Super admin"><slot /></UiAppShell>
</template>
