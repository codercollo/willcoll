<script setup lang="ts">
const branding = useBranding()
const permissions = usePermissions()

const items = computed(() => {
  if (permissions.isLandlord) {
    return [
      { label: 'Reports', to: '/reports/portfolio' },
    ]
  }

  return [
    { label: 'Portfolio', to: '/properties' },
    { label: 'Properties', to: '/properties' },
    { label: 'Ledger', to: '/ledger' },
    { label: 'Reports', to: '/reports/portfolio' },
    { label: 'Agents & Permissions', to: '/agents' },
    { label: 'Settings', to: '/settings' },
  ]
})
</script>

<template>
  <aside class="sidebar">
    <div class="sidebar-brand">
      <img v-if="branding.logo_url" :src="branding.logo_url" :alt="branding.brand_name" class="sidebar-logo">
      <span class="sidebar-name">{{ branding.brand_name }}</span>
    </div>

    <nav class="sidebar-nav">
      <NuxtLink
        v-for="item in items"
        :key="item.label"
        :to="item.to"
        class="sidebar-item"
        active-class="sidebar-item--active"
      >
        {{ item.label }}
      </NuxtLink>
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
</style>
