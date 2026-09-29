<script setup lang="ts">
// Where a QR code lands when it does not work. It never says whether the code
// never existed, was replaced, or belonged to a lease that has ended.
definePageMeta({ layout: 'public', title: 'Code not active' })
useHead({ title: 'This code is not active', meta: [{ name: 'robots', content: 'noindex' }] })
</script>

<template>
  <PayCard>
    <div class="flex flex-col items-center gap-3 text-center">
      <span class="flex size-12 items-center justify-center rounded-full bg-partial-tint text-partial-text"><Icon name="lucide:qr-code" class="size-6" /></span>
      <h2>This code is not active</h2>
      <p class="text-gray-700">This QR code no longer works. Please contact your property manager for a new one.</p>
    </div>
    <div class="mt-6 flex flex-col gap-3">
      <UiButton block large to="/pay">Enter a unit code instead</UiButton>
    </div>
  </PayCard>
</template>
