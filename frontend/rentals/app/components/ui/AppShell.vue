<script setup lang="ts">
// Dark navy sidebar + light canvas (design-tokens 6.1, 6.2, 10). The sidebar is
// the only dark surface in the product.
export interface NavItem { label: string; to: string; icon: string; section?: string; badge?: number }
const props = defineProps<{ brand: string; nav: NavItem[]; userName?: string; userSub?: string }>()

const route = useRoute()
const collapsed = ref(false) // user toggle; forced icon-only below md by CSS
const title = computed(() => (route.meta.title as string | undefined) ?? '')

function active(item: NavItem) {
  return route.path === item.to || route.path.startsWith(item.to + '/')
}

const groups = computed(() => {
  const out: { section?: string; items: NavItem[] }[] = []
  for (const item of props.nav) {
    const last = out[out.length - 1]
    if (last && last.section === item.section) last.items.push(item)
    else out.push({ section: item.section, items: [item] })
  }
  return out
})

const initials = computed(() => (props.userName ?? '?').split(/\s+/).map(w => w[0]).slice(0, 2).join('').toUpperCase())

const auth = useAuthStore()
async function signOut() {
  const wasAdmin = auth.principal === 'admin'
  await auth.logout()
  await navigateTo(wasAdmin ? '/admin/login' : '/auth/login')
}
</script>

<template>
  <div class="min-h-screen bg-surface-canvas">
    <aside
      :class="[
        'fixed inset-y-0 left-0 z-30 flex flex-col bg-surface-sidebar text-text-inverse transition-[width] duration-(--duration-slow) ease-standard',
        collapsed ? 'w-(--sidebar-width-collapsed)' : 'w-(--sidebar-width-collapsed) md:w-(--sidebar-width-expanded)',
      ]"
      aria-label="Main"
    >
      <div class="mb-12 flex h-16 items-center gap-3 px-5 pt-6">
        <span class="flex size-9 shrink-0 items-center justify-center rounded-sm bg-accent-primary"><Icon name="lucide:building-2" class="size-5" /></span>
        <span v-if="!collapsed" class="hidden text-[length:var(--text-h2)] font-semibold md:block">{{ brand }}</span>
      </div>

      <nav class="flex-1 overflow-y-auto px-3">
        <div v-for="(g, gi) in groups" :key="gi">
          <p v-if="g.section" :class="['pt-6 pb-2 pl-5 text-[length:var(--text-caption)] tracking-[0.04em] text-text-inverse-muted uppercase', collapsed ? 'hidden' : 'hidden md:block']">{{ g.section }}</p>
          <NuxtLink
            v-for="item in g.items"
            :key="item.to"
            :to="item.to"
            :title="item.label"
            :aria-current="active(item) ? 'page' : undefined"
            :class="[
              'flex h-11 items-center gap-3 rounded-md px-5 transition-colors duration-(--duration-fast)',
              active(item) ? 'bg-surface-sidebar-active font-semibold text-white' : 'text-text-inverse-muted hover:bg-surface-sidebar-hover hover:text-white',
            ]"
          >
            <Icon :name="item.icon" class="size-5 shrink-0" />
            <span :class="collapsed ? 'hidden' : 'hidden md:inline'">{{ item.label }}</span>
            <span v-if="item.badge" :class="['ml-auto min-w-5 rounded-full bg-unmatched-text px-1.5 text-center text-[length:var(--text-caption)] font-semibold text-white', collapsed ? 'hidden' : 'hidden md:inline']" :aria-label="`${item.badge} waiting`">{{ item.badge }}</span>
          </NuxtLink>
        </div>
      </nav>

      <div class="border-t border-navy-700 p-3">
        <button type="button" class="flex h-11 w-full items-center gap-3 rounded-md px-5 text-text-inverse-muted transition-colors duration-(--duration-fast) hover:bg-surface-sidebar-hover hover:text-white" @click="signOut">
          <Icon name="lucide:log-out" class="size-5 shrink-0" />
          <span :class="collapsed ? 'hidden' : 'hidden md:inline'">Sign out</span>
        </button>
        <button type="button" class="mt-1 hidden h-11 w-full items-center gap-3 rounded-md px-5 text-text-inverse-muted hover:bg-surface-sidebar-hover hover:text-white md:flex" :aria-label="collapsed ? 'Expand sidebar' : 'Collapse sidebar'" @click="collapsed = !collapsed">
          <Icon :name="collapsed ? 'lucide:panel-left-open' : 'lucide:panel-left-close'" class="size-5 shrink-0" />
          <span v-if="!collapsed">Collapse</span>
        </button>
      </div>
    </aside>

    <div :class="['transition-[padding] duration-(--duration-slow) ease-standard', collapsed ? 'pl-(--sidebar-width-collapsed)' : 'pl-(--sidebar-width-collapsed) md:pl-(--sidebar-width-expanded)']">
      <header class="sticky top-0 z-20 flex h-(--topbar-height) items-center justify-between gap-4 bg-surface-canvas px-4 md:px-8">
        <h1 class="truncate">{{ title }}</h1>
        <div class="flex items-center gap-3">
          <slot name="topbar" />
          <div v-if="userName" class="flex items-center gap-3">
            <span class="flex size-9 items-center justify-center rounded-full bg-navy-800 text-[length:var(--text-caption)] font-semibold text-white" aria-hidden="true">{{ initials }}</span>
            <div class="hidden leading-tight sm:block">
              <p class="font-semibold">{{ userName }}</p>
              <p v-if="userSub" class="text-[length:var(--text-caption)] text-gray-500">{{ userSub }}</p>
            </div>
          </div>
        </div>
      </header>
      <main class="mx-auto w-full max-w-(--content-max-width) px-4 pt-6 pb-16 md:px-8 md:pt-10">
        <slot />
      </main>
    </div>
  </div>
</template>
