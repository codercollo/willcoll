<script setup lang="ts">
import { looksLikePhone } from '../utils/payForm'

// Prove you hold a registered phone: enter it, then the SMS code. The product
// supplies the two calls (and its own wording); this owns the two-step flow.
// The API answers the same way for any number, so this screen never says
// whether a phone is known.
const props = withDefaults(defineProps<{
  notice?: string
  requestCode: (phone: string) => Promise<unknown>
  verifyCode: (phone: string, code: string) => Promise<unknown>
  intro?: string
  phoneHelp?: string
}>(), { intro: 'To continue, confirm your phone number. We will text you a code.', phoneHelp: 'Use the phone number registered with us.' })
const emit = defineEmits<{ verified: [] }>()

const ask = useSubmit()
const check = useSubmit()

const phone = ref('')
const code = ref('')
const stage = ref<'phone' | 'code'>('phone')
const cooldown = ref(0)
let timer: ReturnType<typeof setInterval> | undefined

function startCooldown() {
  cooldown.value = 30
  clearInterval(timer)
  timer = setInterval(() => { cooldown.value = Math.max(0, cooldown.value - 1); if (!cooldown.value) clearInterval(timer) }, 1000)
}
onBeforeUnmount(() => clearInterval(timer))

async function sendCode() {
  if (!looksLikePhone(phone.value)) { ask.message.value = 'Enter a Kenyan phone number, for example 0722 000 000.'; return }
  const ok = await ask.run(async () => { await props.requestCode(phone.value.trim()); return true })
  if (ok) { stage.value = 'code'; code.value = ''; check.message.value = ''; startCooldown() }
}

async function verify() {
  const ok = await check.run(async () => { await props.verifyCode(phone.value.trim(), code.value.trim()); return true })
  if (ok) emit('verified')
}

function back() { stage.value = 'phone'; ask.message.value = '' }
</script>

<template>
  <div>
    <UiFormAlert v-if="props.notice" tone="info" class="mb-5">{{ props.notice }}</UiFormAlert>

    <form v-if="stage === 'phone'" class="flex flex-col gap-5" novalidate @submit.prevent="sendCode">
      <p class="text-gray-700">{{ intro }}</p>
      <UiFormAlert v-if="ask.message.value">{{ ask.message.value }}</UiFormAlert>
      <UiField label="Phone number" for="pay-phone" :help="phoneHelp">
        <UiInput id="pay-phone" v-model="phone" type="tel" autocomplete="tel" inputmode="tel" placeholder="0722 000 000" :invalid="!!ask.message.value" />
      </UiField>
      <UiButton type="submit" block large :loading="ask.loading.value" :disabled="!phone.trim()">Send me a code</UiButton>
    </form>

    <form v-else class="flex flex-col gap-5" novalidate @submit.prevent="verify">
      <p class="text-gray-700"><slot name="sent" :phone="phone">If <strong class="tnum">{{ phone }}</strong> is registered, a code is on its way. It lasts a few minutes and works once.</slot></p>
      <UiFormAlert v-if="check.message.value">{{ check.message.value }}</UiFormAlert>
      <UiField label="Code from the SMS" for="pay-code">
        <UiInput id="pay-code" v-model="code" inputmode="numeric" autocomplete="one-time-code" placeholder="123456" :invalid="!!check.message.value" />
      </UiField>
      <UiButton type="submit" block large :loading="check.loading.value" :disabled="code.trim().length < 4">Verify</UiButton>
      <div class="flex items-center justify-between text-gray-500">
        <button type="button" class="font-semibold text-accent-text" @click="back">Use a different number</button>
        <button type="button" class="font-semibold text-accent-text disabled:text-gray-500" :disabled="cooldown > 0 || ask.loading.value" @click="sendCode">{{ cooldown > 0 ? `Resend in ${cooldown}s` : 'Resend code' }}</button>
      </div>
    </form>
  </div>
</template>
