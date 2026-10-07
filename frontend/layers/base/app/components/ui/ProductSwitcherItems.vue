<script setup lang="ts">
import type { SwitcherItem } from '../../utils/appLinks'

// The rows of the product switcher: one per running product, then Account and
// Billing in platform. Other apps are full-page links (different origin); the
// current app's own row is a router link.
defineProps<{
  items: SwitcherItem[]
  current: string
  homeTo: string
  accountTo: string
  billingTo: string
  showBilling: boolean
}>()

const row = 'flex h-11 items-center gap-3 rounded-md px-3 text-text-inverse-muted transition-colors duration-(--duration-fast) hover:bg-surface-sidebar-hover hover:text-white'
</script>

<template>
  <p class="px-3 pt-1 pb-1 text-[length:var(--text-caption)] tracking-[0.04em] text-text-inverse-muted uppercase">Products</p>
  <template v-for="i in items" :key="i.product">
    <NuxtLink v-if="i.product === current" :to="homeTo" role="menuitem" :class="[row, 'bg-surface-sidebar-active font-semibold text-white']" aria-current="true">
      <Icon :name="i.icon" class="size-5 shrink-0" /><span>{{ i.label }}</span>
    </NuxtLink>
    <a v-else :href="i.url" role="menuitem" :class="row">
      <Icon :name="i.icon" class="size-5 shrink-0" /><span>{{ i.label }}</span>
    </a>
  </template>
  <p v-if="!items.length" class="px-3 py-2 text-text-inverse-muted">No products yet.</p>
  <p class="px-3 pt-3 pb-1 text-[length:var(--text-caption)] tracking-[0.04em] text-text-inverse-muted uppercase">Willcoll account</p>
  <NuxtLink :to="accountTo" role="menuitem" :class="row"><Icon name="lucide:user-round" class="size-5 shrink-0" /><span>Account</span></NuxtLink>
  <NuxtLink v-if="showBilling" :to="billingTo" role="menuitem" :class="row"><Icon name="lucide:credit-card" class="size-5 shrink-0" /><span>Billing</span></NuxtLink>
</template>
