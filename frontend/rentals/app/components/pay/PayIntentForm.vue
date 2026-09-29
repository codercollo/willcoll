<script setup lang="ts">
import type { PayBalance } from '~/types/pay'
import { isDepositType, isWholeShillings, MAX_PAYMENT, paymentTotal, payLabel, splitProblem } from '~/utils/pay'

// Balances summary (label left, amount right, status inline) and the amounts
// the tenant wants to pay now (design-tokens 7.7, 7.8).
const props = defineProps<{ balances: PayBalance[]; phone: string; busy?: boolean; error?: string }>()
const emit = defineEmits<{ submit: [lines: { type: string; amount: string }[], phone: string] }>()

const cur = useCurrency()
const amounts = reactive<Record<string, string>>({})
const stkPhone = ref(props.phone)

const payable = computed(() => props.balances.filter(b => b.payable))
const owed = (b: PayBalance) => !cur.isZero(b.balance) && !cur.isNegative(b.balance)

// A whole-shilling amount to suggest: what is owed, rounded up.
function wholeOwed(b: PayBalance) {
  const [w = '0', f = '00'] = b.balance.split('.')
  return String(Number(w) + (Number(f) > 0 ? 1 : 0))
}
function payFull(b: PayBalance) { if (owed(b)) amounts[b.type] = wholeOwed(b) }

const touched = ref(false)
const problem = computed(() => splitProblem(amounts))
const total = computed(() => paymentTotal(amounts))

// A deposit can't be paid ahead: the input is capped at what's still owed
// (whole shillings, rounded up like the "Full" button), and disabled once
// there's nothing left to pay. The server enforces this too (the real
// guard); this is just so the tenant never types a value that will bounce.
const depositCap = (b: PayBalance) => (isDepositType(b.type) ? Number(wholeOwed(b)) : null)
const depositPaid = (b: PayBalance) => isDepositType(b.type) && !owed(b)

const lineError = (b: PayBalance) => {
  if (!touched.value || (amounts[b.type] ?? '') === '') return ''
  if (!isWholeShillings(amounts[b.type]!)) return 'Whole shillings only'
  const cap = depositCap(b)
  if (cap !== null && Number(amounts[b.type]) > cap) return `Cannot exceed the Ksh ${cap.toLocaleString('en-KE')} owed`
  return ''
}

function submit() {
  touched.value = true
  if (problem.value) return
  if (payable.value.some(b => lineError(b))) return
  const lines = payable.value.filter(b => (amounts[b.type] ?? '').trim() !== '').map(b => ({ type: b.type, amount: amounts[b.type]!.trim() }))
  emit('submit', lines, stkPhone.value.trim())
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <ul class="divide-y divide-border-subtle rounded-sm border border-border-default" aria-label="What you owe">
      <li v-for="b in balances" :key="b.type" class="flex items-center justify-between gap-3 px-4 py-3">
        <span class="font-semibold">{{ payLabel(b.type) }}</span>
        <span class="flex items-center gap-3">
          <span :class="['money text-[length:var(--text-money-sm)] font-semibold', owed(b) ? 'text-arrears-text' : 'text-paid-text']">{{ cur.isNegative(b.balance) ? `${cur.format(b.balance.replace('-', ''))} credit` : cur.format(b.balance) }}</span>
          <UiBadge v-if="owed(b)" status="arrears" label="Owing" />
          <UiBadge v-else-if="cur.isNegative(b.balance)" status="paid" label="Paid ahead" />
          <UiBadge v-else status="paid" label="Paid" />
        </span>
      </li>
    </ul>

    <form class="flex flex-col gap-5" novalidate @submit.prevent="submit">
      <p class="font-semibold">How much would you like to pay?</p>
      <UiFormAlert v-if="error">{{ error }}</UiFormAlert>

      <UiField v-for="b in payable" :key="b.type" :label="payLabel(b.type)" :for="`pay-${b.type}`" :error="lineError(b)">
        <UiInput :id="`pay-${b.type}`" v-model="amounts[b.type]" money inputmode="numeric" placeholder="0" :invalid="!!lineError(b)">
          <template #suffix>
            <button v-if="owed(b)" type="button" class="mr-3 shrink-0 text-[length:var(--text-caption)] font-semibold text-accent-text" @click="payFull(b)">Full</button>
          </template>
        </UiInput>
      </UiField>

      <UiField label="M-Pesa number to prompt" for="pay-stk" help="The prompt goes to this phone.">
        <UiInput id="pay-stk" v-model="stkPhone" type="tel" inputmode="tel" />
      </UiField>

      <p v-if="touched && problem" class="flex items-center gap-1 text-[length:var(--text-caption)] text-arrears-text" role="alert"><Icon name="lucide:triangle-alert" class="size-[14px]" />{{ problem }}</p>
      <p v-else class="text-[length:var(--text-caption)] text-gray-500">Whole shillings only. Up to Ksh {{ MAX_PAYMENT.toLocaleString('en-KE') }} at a time. Paying ahead is fine.</p>

      <UiButton type="submit" block large :loading="busy" :disabled="busy || !stkPhone.trim()">{{ total ? `Pay Ksh ${total.toLocaleString('en-KE')}` : 'Pay' }}</UiButton>
    </form>
  </div>
</template>
