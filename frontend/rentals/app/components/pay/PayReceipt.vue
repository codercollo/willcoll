<script setup lang="ts">
import type { PayReceipt } from '~/types/pay'
import { payLabel } from '~/utils/pay'

defineProps<{ receipt: PayReceipt }>()
defineEmits<{ again: [] }>()
const cur = useCurrency()
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-col items-center gap-2 text-center">
      <span class="flex size-12 items-center justify-center rounded-full bg-paid-tint text-paid-text"><Icon name="lucide:check" class="size-6" /></span>
      <h2>Payment received</h2>
      <p class="text-gray-500">Thank you. A confirmation SMS is on its way.</p>
    </div>

    <div class="rounded-sm border border-border-default">
      <div class="flex items-center justify-between border-b border-border-subtle px-4 py-3">
        <span class="text-gray-500">M-Pesa receipt</span><span class="tnum font-semibold">{{ receipt.mpesa_receipt }}</span>
      </div>
      <div class="flex items-center justify-between border-b border-border-subtle px-4 py-3">
        <span class="text-gray-500">For</span><span class="font-semibold">{{ receipt.property_name }} &middot; {{ receipt.unit_code }}</span>
      </div>
      <div class="flex items-center justify-between border-b border-border-subtle px-4 py-3">
        <span class="text-gray-500">Paid</span><span class="tnum font-semibold">{{ formatDateTime(receipt.paid_at) }}</span>
      </div>
      <ul>
        <li v-for="l in receipt.lines" :key="l.type" class="flex items-center justify-between border-b border-border-subtle px-4 py-3">
          <span>{{ payLabel(l.type) }}</span><span class="money font-semibold">{{ cur.format(l.amount) }}</span>
        </li>
      </ul>
      <div class="flex items-center justify-between px-4 py-4">
        <span class="font-semibold">Total (Ksh)</span><span class="money text-[length:var(--text-money-lg)] font-bold">{{ cur.format(receipt.amount) }}</span>
      </div>
    </div>

    <UiButton variant="secondary" block large @click="$emit('again')">Make another payment</UiButton>
  </div>
</template>
