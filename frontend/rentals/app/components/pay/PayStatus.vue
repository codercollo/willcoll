<script setup lang="ts">
import type { IntentStatus } from '~/types/pay'

// Live STK-push progress: Sent to your phone -> Waiting for PIN -> Confirmed.
// Polls the status endpoint every 2.5 s until it is no longer pending. State
// is carried by icon and text, not only by the (motion-gated) line fill.
const props = defineProps<{ intentId: string; amount: string; expiresAt: string; pendingNotice?: string }>()
const emit = defineEmits<{ done: [IntentStatus]; expired: []; retry: [] }>()

const pay = usePay()
const cur = useCurrency()
const state = ref<IntentStatus['status']>('pending')
const reason = ref('')
const polls = ref(0)
let timer: ReturnType<typeof setTimeout> | undefined
let stopped = false

// Confirmed is the last step: mark it complete (index 3) rather than current.
const current = computed(() => (state.value === 'completed' ? 3 : polls.value > 0 ? 1 : 0))
const failed = computed(() => state.value === 'failed' || state.value === 'expired')
const slow = computed(() => state.value === 'pending' && polls.value >= 24) // ~1 minute

async function poll() {
  if (stopped) return
  try {
    const s = await pay.status(props.intentId)
    polls.value++
    state.value = s.status
    if (s.status === 'pending') {
      if (new Date(props.expiresAt).getTime() < Date.now()) { state.value = 'expired'; return }
      timer = setTimeout(poll, 2500)
      return
    }
    reason.value = s.failure_reason ?? ''
    if (s.status === 'completed') emit('done', s)
  } catch (e) {
    if (e instanceof Error && 'isAuth' in e && (e as { isAuth: boolean }).isAuth) { emit('expired'); return }
    timer = setTimeout(poll, 4000) // a blip: keep trying
  }
}

onMounted(poll)
onBeforeUnmount(() => { stopped = true; clearTimeout(timer) })
</script>

<template>
  <div class="flex flex-col gap-6 text-center" role="status" aria-live="polite">
    <div>
      <p class="text-gray-500">Paying</p>
      <p class="money text-[length:var(--text-display)] font-bold">Ksh {{ cur.format(amount) }}</p>
    </div>

    <UiStepper :steps="['Sent to your phone', 'Waiting for PIN', 'Confirmed']" :current="current" :failed="failed" />

    <p v-if="state === 'pending'" class="text-gray-700">{{ pendingNotice || 'Check your phone and enter your M-Pesa PIN.' }}</p>
    <p v-if="slow" class="text-gray-500">This is taking longer than usual. If you have already paid, your payment will still be applied and you can close this page.</p>

    <template v-if="failed">
      <UiFormAlert>{{ state === 'expired' ? 'The request timed out before a PIN was entered.' : (reason || 'The payment was not completed.') }} You have not been charged.</UiFormAlert>
      <UiButton block large @click="emit('retry')">Try again</UiButton>
    </template>
  </div>
</template>
