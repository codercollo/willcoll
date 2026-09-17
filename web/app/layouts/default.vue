<template>
  <div class="app-layout">
    <AppSidebar />
    <div class="app-layout__main">
      <AppHeader />
      <main class="app-layout__content">
        <slot />
      </main>
    </div>
  </div>
</template>

<style scoped>
.app-layout {
  display: flex;
  min-height: 100vh;
  background: var(--surface-flat, #F5F6F8);
}

.app-layout__main {
  flex: 1;
  display: flex;
  flex-direction: column;
}

.app-layout__content {
  flex: 1;
  padding: var(--space-8);
  min-width: 0;
}

@media (max-width: 768px) {
  .app-layout {
    flex-direction: column;
  }
  .app-layout__content {
    padding: var(--space-4);
  }
}
</style>
