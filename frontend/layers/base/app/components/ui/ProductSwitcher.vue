<script setup lang="ts">
import { PRODUCTS, switcherItems } from '../../utils/appLinks'

// The product switcher (build-phases.txt 3l). Lists the products the firm has
// running (staff: only those in their module_access), each a link to that
// app, plus Account and Billing in platform. App URLs come from runtime
// config. `layout="popover"` is the sidebar's dropdown; `layout="list"` is the
// plain list inside the mobile "More" sheet.
const props = withDefaults(defineProps<{ layout?: 'popover' | 'list'; collapsed?: boolean }>(), { layout: 'popover', collapsed: false })

const auth = useAuthStore()
const links = useAppLinks()
const config = useRuntimeConfig().public

onMounted(() => { auth.fetchModules() })

const items = computed(() => switcherItems(auth.modules ?? [], auth.staff, links.apps))
const current = computed(() => PRODUCTS.find(p => p.key === config.product))
const currentLabel = computed(() => current.value?.label ?? 'Account')
const open = ref(false)

// Account and Billing live in platform; inside platform they are ordinary routes.
const accountTo = computed(() => (links.signInHere ? '/account' : links.accountUrl()))
const billingTo = computed(() => (links.signInHere ? '/billing' : links.platformUrl('/billing')))
// Only the firm's own login and owners manage billing (system-design A.1).
const showBilling = computed(() => auth.isOwner)
const homeTo = computed(() => (links.signInHere ? '/' : config.guard.managerHome))

const route = useRoute()
watch(() => route.path, () => { open.value = false })
</script>

<template>
  <!-- Admins have no products to switch between. -->
  <div v-if="auth.principal === 'manager'" :class="props.layout === 'popover' ? 'relative px-3' : ''">
    <template v-if="props.layout === 'popover'">
      <button
        type="button"
        :title="'Switch product'"
        :aria-expanded="open"
        aria-haspopup="menu"
        class="flex h-11 w-full items-center gap-3 rounded-md px-5 text-text-inverse-muted transition-colors duration-(--duration-fast) hover:bg-surface-sidebar-hover hover:text-white"
        data-testid="product-switcher"
        @click="open = !open"
      >
        <Icon name="lucide:layout-grid" class="size-5 shrink-0" />
        <span v-if="!props.collapsed" class="truncate">{{ currentLabel }}</span>
        <Icon v-if="!props.collapsed" name="lucide:chevrons-up-down" class="ml-auto size-4 shrink-0" />
      </button>
      <div
        v-if="open"
        role="menu"
        aria-label="Products"
        class="absolute top-full left-3 z-50 mt-1 w-60 rounded-md border border-navy-700 bg-surface-sidebar p-2 text-text-inverse shadow-modal"
        @keydown.esc="open = false"
      >
        <UiProductSwitcherItems :items="items" :current="config.product" :home-to="homeTo" :account-to="accountTo" :billing-to="billingTo" :show-billing="showBilling" />
      </div>
    </template>
    <UiProductSwitcherItems v-else :items="items" :current="config.product" :home-to="homeTo" :account-to="accountTo" :billing-to="billingTo" :show-billing="showBilling" />
  </div>
</template>
