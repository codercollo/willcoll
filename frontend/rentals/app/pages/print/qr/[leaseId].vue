<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { Lease, Property, Unit } from '~/types/api'
import type { UnitQr } from '~/stores/qr'

// A6 door sticker (105 x 148 mm): property, unit, the QR, "Scan to pay rent"
// and the short code. Black on white; the QR is 60 mm including its quiet zone
// (the requirement is 25 mm). Open with ?auto=1 to print as soon as it loads.
definePageMeta({ layout: 'print', title: 'Print sticker' })

// The paper size is injected at runtime: the build's CSS optimiser drops the
// `size` descriptor from a stylesheet @page rule and would print on Letter.
useHead({ style: [{ key: 'a6-page', innerHTML: '@page { size: 105mm 148mm; margin: 0; }' }] })

const route = useRoute()
const api = useApi()
const qr = useQr()
const leaseId = String(route.params.leaseId)

const code = ref<UnitQr | null>(null)
const unit = ref<Unit | null>(null)
const property = ref<Property | null>(null)
const imageUrl = ref('')
const state = ref<'loading' | 'ready' | 'error' | 'unavailable' | 'ended'>('loading')
const error = ref('')

onMounted(async () => {
  try {
    const { lease } = await api.get<{ lease: Lease }>(`/leases/${leaseId}`)
    if (lease.status !== 'active') { state.value = 'ended'; return }
    const [u, c] = await Promise.all([api.get<{ unit: Unit }>(`/units/${lease.unit_id}`), qr.get(leaseId)])
    unit.value = u.unit
    code.value = c
    property.value = (await api.get<{ property: Property }>(`/properties/${u.unit.property_id}`)).property
    imageUrl.value = URL.createObjectURL(await qr.image(leaseId))
    state.value = 'ready'
  } catch (e) {
    if (e instanceof ApiError && e.status === 503) state.value = 'unavailable'
    else if (e instanceof ApiError && e.status === 409) state.value = 'ended'
    else { state.value = 'error'; error.value = e instanceof Error ? e.message : 'Could not prepare the sticker.' }
  }
})
onBeforeUnmount(() => { if (imageUrl.value) URL.revokeObjectURL(imageUrl.value) })

// Wait for the image itself before the print dialog, or the sticker prints blank.
function printNow() { window.print() }
function loaded() { if (route.query.auto === '1') setTimeout(() => window.print(), 150) }
</script>

<template>
  <div class="mx-auto flex max-w-xl flex-col items-center gap-6 px-4 py-8 print:max-w-none print:p-0">
    <!-- Screen-only controls -->
    <div class="flex w-full flex-wrap items-center justify-between gap-3 print:hidden">
      <NuxtLink v-if="unit" :to="`/properties/${unit.property_id}/units/${unit.id}`" class="inline-flex items-center gap-1 text-gray-500 hover:text-gray-900"><Icon name="lucide:chevron-left" class="size-4" />Back to unit</NuxtLink>
      <span v-else />
      <UiButton :disabled="state !== 'ready'" @click="printNow"><Icon name="lucide:printer" class="size-4" />Print sticker (A6)</UiButton>
    </div>

    <UiSkeleton v-if="state === 'loading'" block class="!h-96 w-full" />
    <UiFormAlert v-else-if="state === 'unavailable'" tone="info" class="w-full">QR codes are not available on this server yet. The administrator needs to set QR_BASE_URL.</UiFormAlert>
    <UiFormAlert v-else-if="state === 'ended'" tone="info" class="w-full">This lease has ended, so it has no unit code to print.</UiFormAlert>
    <UiFormAlert v-else-if="state === 'error'" class="w-full">{{ error }}</UiFormAlert>

    <!-- The sticker: physical units, because this is a printed document. -->
    <article v-else-if="state === 'ready' && code && unit && property" class="sticker flex flex-col items-center justify-between bg-white text-center text-gray-900 shadow-card print:shadow-none" aria-label="Unit payment sticker">
      <header class="w-full">
        <p class="text-[length:var(--text-h2)] font-semibold tracking-widest uppercase">{{ property.name }}</p>
        <p class="mt-3 text-[length:var(--text-display)] leading-none font-bold">Unit {{ unit.unit_code }}</p>
        <div class="mx-auto mt-4 h-0.5 w-16 bg-gray-900" aria-hidden="true" />
      </header>

      <img :src="imageUrl" :alt="`QR code to pay rent for unit ${unit.unit_code}`" class="sticker-qr block" @load="loaded">

      <footer class="w-full">
        <p class="text-[length:var(--text-h1)] leading-tight font-bold">Scan to pay rent</p>
        <p class="mt-1 text-[length:var(--text-body)] text-gray-700">Pay with M-Pesa on your phone</p>
        <div class="mx-auto mt-5 w-fit rounded-md border-2 border-gray-900 px-5 py-2">
          <p class="text-[length:var(--text-caption)] text-gray-700">Can't scan? Enter this code</p>
          <p class="tnum text-[length:var(--text-h2)] font-bold tracking-widest" data-testid="sticker-code">{{ code.short_code }}</p>
        </div>
      </footer>
    </article>
  </div>
</template>

<style>
.sticker { width: 105mm; height: 148mm; padding: 12mm 8mm 11mm; box-sizing: border-box; }
.sticker-qr { width: 60mm; height: 60mm; image-rendering: pixelated; }

@media print {
  html, body { background: white !important; }
  .sticker { break-inside: avoid; }
}
</style>
