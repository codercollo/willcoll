<template>
  <!-- Auth pages and the tenant pay page: light only, no navigation chrome. -->
  <div class="flex min-h-screen flex-col items-center justify-center bg-surface-canvas px-4 py-10">
    <div class="mb-8 flex items-center gap-3">
      <span class="flex size-10 items-center justify-center rounded-sm bg-accent-primary text-white"><Icon name="lucide:building-2" class="size-6" /></span>
      <span class="text-[length:var(--text-h2)] font-semibold">Willcoll</span>
    </div>
    <slot />
  </div>
</template>
