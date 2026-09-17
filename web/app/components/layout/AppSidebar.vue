<script setup lang="ts">
const branding = useBranding()
const route = useRoute()
const base = computed(() => `/${route.params.slug}`)

// Every nav item declares the RBAC action that gates it (spec §2.1) — a
// Landlord never sees "Record Payment"/property-edit style items because
// their role bundle never includes those actions, not because they're
// disabled-and-confusing.
const items = computed(() => [
  { label: 'Properties', to: `${base.value}/properties`, action: 'view_portfolio' },
  { label: 'Ledger', to: `${base.value}/ledger`, action: 'view_ledger' },
  { label: 'Reports', to: `${base.value}/reports/portfolio`, action: 'view_financial_reports' },
  { label: 'Agents & Permissions', to: `${base.value}/agents`, action: 'invite_agent' },
  { label: 'Settings', to: `${base.value}/settings`, action: 'configure_sms_templates' },
])
</script>

<template>
  <aside class="sidebar">
    <div class="sidebar-brand">
      <img v-if="branding?.logo_url" :src="branding?.logo_url" :alt="branding?.brand_name" class="sidebar-logo">
      <span class="sidebar-name">{{ branding?.brand_name }}</span>
    </div>

    <nav class="sidebar-nav">
      <RoleGate v-for="item in items" :key="item.label" :action="item.action">
        <NuxtLink :to="item.to" class="sidebar-item" active-class="sidebar-item--active">
          {{ item.label }}
        </NuxtLink>
      </RoleGate>
    </nav>
  </aside>
</template>

<style scoped>
.sidebar {
  width: var(--layout-sidebar-width, 260px);
  height: 100vh;
  background: var(--sidebar-bg, #FFFFFF);
  border-right: var(--border-hairline, 1px solid #E2E5E9);
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}

.sidebar-brand {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4);
  min-height: var(--layout-header-height, 68px);
}

.sidebar-logo { max-height: 32px; }
.sidebar-name {
  font-family: var(--font-display, serif);
  color: var(--color-text-primary, #1F2320);
}

.sidebar-nav {
  display: grid;
  gap: var(--space-1);
  padding: var(--space-2);
}

.sidebar-item {
  position: relative;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-sm, 4px);
  color: var(--color-text-primary, #1F2320);
  text-decoration: none;
}

.sidebar-item--active {
  background: var(--sidebar-item-active-bg, rgba(245,181,145,0.10));
}

.sidebar-item--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 8px;
  bottom: 8px;
  width: 3px;
  border-radius: 3px;
  background: var(--sidebar-item-active-bar, #E8702A);
}

/* Narrow viewports: sidebar collapses to an icon-free, top-docked strip
   instead of eating the whole screen (layout stays usable on a phone). */
@media (max-width: 768px) {
  .sidebar {
    width: 100%;
    height: auto;
    border-right: 0;
    border-bottom: var(--border-hairline, 1px solid #E2E5E9);
  }
  .sidebar-nav {
    grid-auto-flow: column;
    overflow-x: auto;
  }
}
</style>
