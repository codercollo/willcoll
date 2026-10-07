<script setup lang="ts">
// Dark navy sidebar + light canvas (design-tokens 6.1, 6.2, 10). The sidebar is
// the only dark surface in the product. Below md it becomes a bottom
// navigation bar: the main destinations as icon + label tabs, and a "More"
// sheet for the account section and sign-out.
export interface NavItem { label: string; to: string; icon: string; section?: string; badge?: number }
const props = withDefaults(defineProps<{ brand: string; brandIcon?: string; nav: NavItem[]; userName?: string; userSub?: string }>(), { brandIcon: 'lucide:building-2' })

const route = useRoute()
const collapsed = ref(false) // user toggle (md and up)
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

// Bottom bar: the first MAX_TABS unsectioned items; everything else (the
// account section, overflow, sign-out) lives in the More sheet.
const MAX_TABS = 4
const tabs = computed(() => props.nav.filter(i => !i.section).slice(0, MAX_TABS))
const overflow = computed(() => props.nav.filter(i => !tabs.value.includes(i)))
const overflowGroups = computed(() => {
  const out: { section?: string; items: NavItem[] }[] = []
  for (const item of overflow.value) {
    const last = out[out.length - 1]
    if (last && last.section === item.section) last.items.push(item)
    else out.push({ section: item.section, items: [item] })
  }
  return out
})
const moreActive = computed(() => overflow.value.some(active))
const moreBadge = computed(() => overflow.value.reduce((a, i) => a + (i.badge ?? 0), 0))

const moreOpen = ref(false)
watch(() => route.path, () => { moreOpen.value = false })

const initials = computed(() => (props.userName ?? '?').split(/\s+/).map(w => w[0]).slice(0, 2).join('').toUpperCase())

const auth = useAuthStore()
async function signOut() {
  moreOpen.value = false
  const wasAdmin = auth.principal === 'admin'
  await auth.logout()
  // The sign-in pages are platform's; from a product app that is a full-page redirect.
  await useAppLinks().goToLogin({ admin: wasAdmin })
}
</script>

<template>
  <div class="min-h-screen bg-surface-canvas">
    <aside
      :class="[
        'fixed inset-y-0 left-0 z-30 hidden flex-col bg-surface-sidebar text-text-inverse transition-[width] duration-(--duration-slow) ease-standard md:flex',
        collapsed ? 'w-(--sidebar-width-collapsed)' : 'w-(--sidebar-width-expanded)',
      ]"
      aria-label="Main"
    >
      <div class="mb-12 flex h-16 items-center gap-3 px-5 pt-6">
        <span class="flex size-9 shrink-0 items-center justify-center rounded-sm bg-accent-primary"><Icon :name="brandIcon" class="size-5" /></span>
        <span v-if="!collapsed" class="text-[length:var(--text-h2)] font-semibold">{{ brand }}</span>
      </div>

      <UiProductSwitcher :collapsed="collapsed" class="mb-4" />

      <nav class="flex-1 overflow-y-auto px-3">
        <div v-for="(g, gi) in groups" :key="gi">
          <p v-if="g.section && !collapsed" class="pt-6 pb-2 pl-5 text-[length:var(--text-caption)] tracking-[0.04em] text-text-inverse-muted uppercase">{{ g.section }}</p>
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
            <span v-if="!collapsed">{{ item.label }}</span>
            <span v-if="item.badge && !collapsed" class="ml-auto min-w-5 rounded-full bg-unmatched-text px-1.5 text-center text-[length:var(--text-caption)] font-semibold text-white" :aria-label="`${item.badge} waiting`">{{ item.badge }}</span>
          </NuxtLink>
        </div>
      </nav>

      <div class="border-t border-navy-700 p-3">
        <button type="button" class="flex h-11 w-full items-center gap-3 rounded-md px-5 text-text-inverse-muted transition-colors duration-(--duration-fast) hover:bg-surface-sidebar-hover hover:text-white" @click="signOut">
          <Icon name="lucide:log-out" class="size-5 shrink-0" />
          <span v-if="!collapsed">Sign out</span>
        </button>
        <button type="button" class="mt-1 flex h-11 w-full items-center gap-3 rounded-md px-5 text-text-inverse-muted hover:bg-surface-sidebar-hover hover:text-white" :aria-label="collapsed ? 'Expand sidebar' : 'Collapse sidebar'" @click="collapsed = !collapsed">
          <Icon :name="collapsed ? 'lucide:panel-left-open' : 'lucide:panel-left-close'" class="size-5 shrink-0" />
          <span v-if="!collapsed">Collapse</span>
        </button>
      </div>
    </aside>

    <div :class="['transition-[padding] duration-(--duration-slow) ease-standard', collapsed ? 'md:pl-(--sidebar-width-collapsed)' : 'md:pl-(--sidebar-width-expanded)']">
      <header class="sticky top-0 z-20 flex h-(--topbar-height) items-center justify-between gap-3 bg-surface-canvas px-4 md:gap-4 md:px-8">
        <div class="flex min-w-0 items-center gap-3">
          <span class="flex size-8 shrink-0 items-center justify-center rounded-sm bg-accent-primary text-white md:hidden" aria-hidden="true"><Icon :name="brandIcon" class="size-5" /></span>
          <h1 class="truncate">{{ title }}</h1>
        </div>
        <div class="flex shrink-0 items-center gap-3">
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
      <main class="mx-auto w-full max-w-(--content-max-width) px-4 pt-4 pb-[calc(var(--bottom-nav-height)+env(safe-area-inset-bottom)+var(--spacing)*6)] md:px-8 md:pt-10 md:pb-16">
        <slot />
      </main>
    </div>

    <!-- Mobile: the "More" sheet, over the page and above the tab bar. -->
    <Transition
      enter-active-class="transition duration-(--duration-base) ease-standard"
      leave-active-class="transition duration-(--duration-base) ease-standard"
      enter-from-class="opacity-0"
      leave-to-class="opacity-0"
    >
      <div v-if="moreOpen" class="fixed inset-0 z-40 bg-navy-950/50 md:hidden" @click.self="moreOpen = false" @keydown.esc="moreOpen = false">
        <div
          id="more-menu"
          role="dialog"
          aria-label="More"
          class="absolute inset-x-0 bottom-[calc(var(--bottom-nav-height)+env(safe-area-inset-bottom))] max-h-[70vh] overflow-y-auto rounded-t-lg bg-surface-sidebar p-3 text-text-inverse shadow-modal"
        >
          <div v-if="userName" class="flex items-center gap-3 border-b border-navy-700 px-3 pt-2 pb-4">
            <span class="flex size-10 shrink-0 items-center justify-center rounded-full bg-navy-800 text-[length:var(--text-caption)] font-semibold text-white" aria-hidden="true">{{ initials }}</span>
            <div class="min-w-0 leading-tight">
              <p class="truncate font-semibold">{{ userName }}</p>
              <p v-if="userSub" class="truncate text-[length:var(--text-caption)] text-text-inverse-muted">{{ userSub }}</p>
            </div>
          </div>
          <UiProductSwitcher layout="list" class="border-b border-navy-700 pb-2" />
          <div v-for="(g, gi) in overflowGroups" :key="gi">
            <p v-if="g.section" class="pt-4 pb-1 pl-3 text-[length:var(--text-caption)] tracking-[0.04em] text-text-inverse-muted uppercase">{{ g.section }}</p>
            <NuxtLink
              v-for="item in g.items"
              :key="item.to"
              :to="item.to"
              :aria-current="active(item) ? 'page' : undefined"
              :class="[
                'flex h-12 items-center gap-3 rounded-md px-3',
                active(item) ? 'bg-surface-sidebar-active font-semibold text-white' : 'text-text-inverse-muted active:bg-surface-sidebar-hover',
              ]"
            >
              <Icon :name="item.icon" class="size-5 shrink-0" />
              <span>{{ item.label }}</span>
              <span v-if="item.badge" class="ml-auto min-w-5 rounded-full bg-unmatched-text px-1.5 text-center text-[length:var(--text-caption)] font-semibold text-white" :aria-label="`${item.badge} waiting`">{{ item.badge }}</span>
            </NuxtLink>
          </div>
          <button type="button" class="mt-2 flex h-12 w-full items-center gap-3 rounded-md border-t border-navy-700 px-3 text-text-inverse-muted active:bg-surface-sidebar-hover" @click="signOut">
            <Icon name="lucide:log-out" class="size-5 shrink-0" />
            <span>Sign out</span>
          </button>
        </div>
      </div>
    </Transition>

    <!-- Mobile: bottom navigation bar. -->
    <nav class="fixed inset-x-0 bottom-0 z-40 flex h-[calc(var(--bottom-nav-height)+env(safe-area-inset-bottom))] items-stretch gap-1 border-t border-navy-700 bg-surface-sidebar px-2 pb-[env(safe-area-inset-bottom)] text-text-inverse md:hidden" aria-label="Main">
      <NuxtLink
        v-for="item in tabs"
        :key="item.to"
        :to="item.to"
        :aria-current="active(item) ? 'page' : undefined"
        :class="[
          'relative my-1.5 flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 rounded-md transition-colors duration-(--duration-fast)',
          active(item) ? 'bg-surface-sidebar-active font-semibold text-white' : 'text-text-inverse-muted active:bg-surface-sidebar-hover',
        ]"
      >
        <Icon :name="item.icon" class="size-5 shrink-0" />
        <span class="max-w-full truncate px-1 text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)]">{{ item.label }}</span>
        <span v-if="item.badge" class="absolute top-0.5 left-1/2 ml-1.5 min-w-4 rounded-full bg-unmatched-text px-1 text-center text-[length:var(--text-caption)] leading-4 font-semibold text-white" :aria-label="`${item.badge} waiting`">{{ item.badge }}</span>
      </NuxtLink>
      <button
        type="button"
        :aria-expanded="moreOpen"
        aria-controls="more-menu"
        :class="[
          'relative my-1.5 flex min-w-0 flex-1 flex-col items-center justify-center gap-0.5 rounded-md transition-colors duration-(--duration-fast)',
          moreOpen || moreActive ? 'bg-surface-sidebar-active font-semibold text-white' : 'text-text-inverse-muted active:bg-surface-sidebar-hover',
        ]"
        @click="moreOpen = !moreOpen"
      >
        <Icon :name="moreOpen ? 'lucide:x' : 'lucide:menu'" class="size-5 shrink-0" />
        <span class="text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)]">More</span>
        <span v-if="moreBadge && !moreOpen" class="absolute top-0.5 left-1/2 ml-1.5 min-w-4 rounded-full bg-unmatched-text px-1 text-center text-[length:var(--text-caption)] leading-4 font-semibold text-white" :aria-label="`${moreBadge} waiting`">{{ moreBadge }}</span>
      </button>
    </nav>
  </div>
</template>
