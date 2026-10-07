<script setup lang="ts">
import type { NavItem } from '#base/components/ui/AppShell.vue'

const auth = useAuthStore()
const links = useAppLinks()
const payments = usePayments()
const onboarding = useOnboarding()

// The badge is the number of payments waiting for the manager (the review queue).
onMounted(() => {
  payments.refreshCount().catch(() => {})
  onboarding.fetchStatus().catch(() => {}) // drives the Setup guide item
})

const nav = computed<NavItem[]>(() => [
  // The setup guide stays in the menu until the essential steps are done.
  ...(onboarding.status && !onboarding.status.complete ? [{ label: 'Setup guide', to: '/onboarding', icon: 'lucide:list-checks', badge: onboarding.remaining }] : []),
  { label: 'Properties', to: '/properties', icon: 'lucide:building-2' },
  { label: 'Payments', to: '/payments', icon: 'lucide:banknote', badge: payments.waiting },
  { label: 'Landlords', to: '/landlords', icon: 'lucide:users' },
  // Account and billing belong to the platform app (build-phases.txt 3l).
  { label: 'Account', to: links.accountUrl(), icon: 'lucide:user-round', section: 'Account' },
  // Billing is owner-only in platform (requireOwner), so staff are not offered it.
  ...(auth.isOwner ? [{ label: 'Billing', to: links.billingUrl(), icon: 'lucide:credit-card', section: 'Account' }] : []),
])
</script>

<template>
  <UiAppShell brand="Willcoll" :nav="nav" :user-name="auth.manager?.firm_name" :user-sub="auth.manager?.email"><UiTrialBanner /><slot /></UiAppShell>
</template>
