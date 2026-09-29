<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { PayBalance, PayIntent, PayReceipt, PayUnit } from '~/types/pay'

// The one screen a tenant ever sees (design-tokens 7.8): a single 480px card.
definePageMeta({ layout: 'public', title: 'Pay' })

const route = useRoute()
const pay = usePay()
const slug = String(route.params.propertySlug)
const unitCode = String(route.params.unitCode)

type Step = 'loading' | 'missing' | 'unavailable' | 'otp' | 'pay' | 'waiting' | 'receipt'
const step = ref<Step>('loading')
const unit = ref<PayUnit | null>(null)
const balances = ref<PayBalance[]>([])
const intent = ref<PayIntent | null>(null)
const receipt = ref<PayReceipt | null>(null)
const notice = ref('')
const submit = useSubmit()
const loadingBalances = ref(false)
const loadError = ref('')

useHead({ title: computed(() => (unit.value ? `Pay ${unit.value.property_name} ${unit.value.unit_code}` : 'Pay rent')) })

// Any 401 from a paid call means the verified session is gone: verify again.
function reverify(message = 'For your safety, please verify your phone again.') {
  pay.forget()
  notice.value = message
  step.value = 'otp'
}

async function loadBalances() {
  loadingBalances.value = true
  loadError.value = ''
  try {
    balances.value = await pay.balances()
    step.value = 'pay'
  } catch (e) {
    if (e instanceof ApiError && e.isAuth) reverify()
    else loadError.value = e instanceof Error ? e.message : 'Could not load your balances.'
  } finally { loadingBalances.value = false }
}

onMounted(async () => {
  pay.open(slug, unitCode)
  try {
    unit.value = await pay.info()
  } catch (e) {
    step.value = e instanceof ApiError && e.status === 404 ? 'missing' : 'unavailable'
    return
  }
  if (!unit.value.can_pay) { step.value = 'unavailable'; return }
  if (pay.session) await loadBalances()
  else step.value = 'otp'
})

const pendingNotice = ref('')

async function startPayment(lines: { type: string; amount: string }[], phone: string) {
  const res = await submit.run(() => pay.createIntent(lines, phone))
  if (res) {
    intent.value = res.intent
    // The push may have already reached the phone even though the request
    // to us timed out; the backend hands back a softer message for that
    // case instead of the default "check your phone and enter your PIN".
    pendingNotice.value = res.message
    step.value = 'waiting'
  } else if (submit.status.value === 401) reverify()
}

function done(s: { receipt?: PayReceipt }) {
  if (s.receipt) { receipt.value = s.receipt; step.value = 'receipt' }
}

async function again() {
  intent.value = null; receipt.value = null; submit.message.value = ''; pendingNotice.value = ''
  await loadBalances()
}

function notYou() { pay.forget(); notice.value = ''; step.value = 'otp' }
</script>

<template>
  <PayCard>
    <template v-if="step === 'loading'"><UiSkeleton :lines="4" /></template>

    <template v-else-if="step === 'missing'">
      <h2>We could not find that unit</h2>
      <p class="mt-2 text-gray-700">Check the link you were given, or ask your property manager to send it again.</p>
    </template>

    <template v-else-if="step === 'unavailable'">
      <h2 v-if="unit">{{ unit.property_name }} &middot; {{ unit.unit_code }}</h2>
      <h2 v-else>Payments are not available</h2>
      <UiFormAlert tone="info" class="mt-4">{{ unit ? 'This property is not set up to take online payments yet. Please pay your property manager directly.' : 'Payments are not available right now. Please try again later.' }}</UiFormAlert>
    </template>

    <template v-else>
      <header class="mb-6">
        <h2>{{ unit?.property_name }}</h2>
        <p class="text-gray-500">Unit <span class="tnum font-semibold text-gray-900">{{ unit?.unit_code }}</span></p>
      </header>

      <PayOtpChallenge v-if="step === 'otp'" :notice="notice" @verified="loadBalances" />

      <template v-else-if="step === 'pay'">
        <UiFormAlert v-if="loadError" class="mb-5">{{ loadError }} <button type="button" class="font-semibold text-accent-text" @click="loadBalances">Retry</button></UiFormAlert>
        <UiSkeleton v-if="loadingBalances" :lines="4" />
        <PayIntentForm v-else :balances="balances" :phone="pay.session?.phone ?? ''" :busy="submit.loading.value" :error="submit.message.value" @submit="startPayment" />
        <p class="mt-6 text-center text-gray-500"><button type="button" class="font-semibold text-accent-text" @click="notYou">Not you? Use a different number</button></p>
      </template>

      <PayStatus v-else-if="step === 'waiting' && intent" :intent-id="intent.id" :amount="intent.amount" :expires-at="intent.expires_at" :pending-notice="pendingNotice" @done="done" @expired="reverify()" @retry="again" />

      <PayReceipt v-else-if="step === 'receipt' && receipt" :receipt="receipt" @again="again" />
    </template>
  </PayCard>
</template>
