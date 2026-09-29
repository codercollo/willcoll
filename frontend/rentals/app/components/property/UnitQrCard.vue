<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { Lease } from '~/types/api'
import type { UnitQr } from '~/stores/qr'

// The unit sticker for tenant payments. The QR holds only a URL: nothing about
// rent, the unit or the tenant is in it. The tenant still verifies by SMS.
const props = defineProps<{ lease: Lease; unitCode: string; propertyName?: string }>()

const qr = useQr()
const toast = useToast()
const cur = useCurrency()

const code = ref<UnitQr | null>(null)
const imageUrl = ref('')
const loading = ref(true)
const error = ref('')
const unavailable = ref(false) // QR_BASE_URL not set on this server (503)

async function load() {
  loading.value = true
  error.value = ''
  unavailable.value = false
  try {
    code.value = await qr.get(props.lease.id)
    await showImage()
  } catch (e) {
    if (e instanceof ApiError && e.status === 503) unavailable.value = true
    else error.value = e instanceof Error ? e.message : 'Could not load the QR code.'
  } finally { loading.value = false }
}

async function showImage() {
  if (imageUrl.value) URL.revokeObjectURL(imageUrl.value)
  imageUrl.value = URL.createObjectURL(await qr.image(props.lease.id))
}

onMounted(load)
watch(() => props.lease.id, load)
onBeforeUnmount(() => { if (imageUrl.value) URL.revokeObjectURL(imageUrl.value) })

// ---- actions ---------------------------------------------------------------
function download() {
  if (!imageUrl.value || !code.value) return
  const a = document.createElement('a')
  a.href = imageUrl.value
  a.download = `willcoll-unit-${props.unitCode}-${code.value.short_code}.png`
  a.click()
}

function printSticker() {
  const url = useRouter().resolve({ path: `/print/qr/${props.lease.id}`, query: { auto: '1' } }).href
  window.open(url, '_blank')
}

const copied = ref(false)
async function copyCode() {
  if (!code.value) return
  try { await navigator.clipboard.writeText(code.value.short_code); copied.value = true; setTimeout(() => (copied.value = false), 1500) } catch { /* clipboard blocked */ }
}

const confirm = ref(false)
const rotating = useSubmit()
async function rotate() {
  const next = await rotating.run(() => qr.rotate(props.lease.id))
  if (!next) return
  code.value = next
  confirm.value = false
  await showImage()
  toast.success('New code issued. The old sticker no longer works.')
}
</script>

<template>
  <UiCard>
    <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2>QR code</h2>
        <p class="text-gray-500">Stick it on the door. Tenants scan it to pay, then confirm their phone by SMS.</p>
      </div>
    </div>

    <UiFormAlert v-if="unavailable" tone="info">QR codes are not available on this server yet. The administrator needs to set <code class="rounded-sm bg-gray-100 px-1">QR_BASE_URL</code>.</UiFormAlert>
    <UiFormAlert v-else-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>

    <div v-else-if="loading" class="grid gap-6 sm:grid-cols-[220px_1fr]"><UiSkeleton block class="!h-56" /><UiSkeleton :lines="5" /></div>

    <div v-else-if="code" class="grid items-start gap-6 sm:grid-cols-[220px_1fr]">
      <div class="mx-auto w-full max-w-[220px] rounded-md border border-border-default bg-white p-2">
        <img :src="imageUrl" :alt="`QR code for unit ${unitCode}. Scan to pay rent.`" width="200" height="200" class="block h-auto w-full">
      </div>

      <div class="flex flex-col gap-5">
        <dl class="grid grid-cols-2 gap-x-6 gap-y-4">
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Unit</dt><dd class="font-semibold">{{ unitCode }}<span v-if="propertyName" class="font-normal text-gray-500"> &middot; {{ propertyName }}</span></dd></div>
          <div>
            <dt class="text-[length:var(--text-caption)] text-gray-500">Short code</dt>
            <dd class="flex items-center gap-2">
              <span class="tnum text-[length:var(--text-h3)] font-semibold tracking-wider" data-testid="short-code">{{ code.short_code }}</span>
              <UiButton variant="icon" :label="copied ? 'Copied' : 'Copy short code'" @click="copyCode"><Icon :name="copied ? 'lucide:check' : 'lucide:copy'" class="size-4" /></UiButton>
            </dd>
          </div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Monthly rent</dt><dd class="money font-semibold">Ksh {{ cur.format(lease.rent_amount) }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Deposits</dt><dd class="money font-semibold">Rent {{ cur.format(lease.rent_deposit_amount) }} &middot; Water {{ cur.format(lease.water_deposit_amount) }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Scans</dt><dd class="tnum font-semibold">{{ code.scan_count }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Last scanned</dt><dd class="font-semibold">{{ code.last_scanned_at ? formatDateTime(code.last_scanned_at) : 'Never' }}</dd></div>
        </dl>

        <div class="flex flex-wrap gap-3">
          <UiButton variant="secondary" @click="download"><Icon name="lucide:download" class="size-4" />Download PNG</UiButton>
          <UiButton @click="printSticker"><Icon name="lucide:printer" class="size-4" />Print sticker</UiButton>
          <UiButton variant="destructive" @click="confirm = true"><Icon name="lucide:refresh-cw" class="size-4" />Rotate</UiButton>
        </div>
      </div>
    </div>

    <UiConfirmDialog v-model="confirm" title="Rotate this QR code?" text="A new code is issued and the old sticker will stop working immediately. You will need to print and replace it." confirm-label="Rotate code" destructive :loading="rotating.loading.value" @confirm="rotate">
      <UiFormAlert v-if="rotating.message.value" class="mt-4">{{ rotating.message.value }}</UiFormAlert>
    </UiConfirmDialog>
  </UiCard>
</template>
