<script setup lang="ts">
import type { LedgerEntry } from '~/types/billing'

// Append-only ledger view (rule R7): entries are never edited or deleted. A
// wrong one is reversed; the original stays, struck through, beside its mirror.
defineProps<{ entries: LedgerEntry[] }>()
defineEmits<{ reverse: [LedgerEntry] }>()
const cur = useCurrency()

const isReversal = (e: LedgerEntry) => /REVERSAL/i.test(e.transaction_type)
const canReverse = (e: LedgerEntry) => !e.reversed && !isReversal(e)
</script>

<template>
  <div class="card overflow-hidden">
    <div class="overflow-x-auto">
      <table class="w-full border-separate border-spacing-0 text-left">
        <thead>
          <tr class="h-10">
            <th scope="col" class="th-text border-b border-border-default bg-gray-50 px-5">Date</th>
            <th scope="col" class="th-text border-b border-border-default bg-gray-50 px-5">Description</th>
            <th scope="col" class="th-text border-b border-border-default bg-gray-50 px-5 text-right">Debit</th>
            <th scope="col" class="th-text border-b border-border-default bg-gray-50 px-5 text-right">Credit</th>
            <th scope="col" class="th-text border-b border-border-default bg-gray-50 px-5 text-right">Balance</th>
            <th scope="col" class="w-12 border-b border-border-default bg-gray-50"><span class="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!entries.length"><td colspan="6" class="px-5 py-12 text-center text-gray-500">No entries yet.</td></tr>
          <tr v-for="e in entries" :key="e.id" :class="['h-12 hover:bg-blue-50', e.reversed && 'text-gray-500']">
            <td class="tnum border-b border-border-subtle px-5 py-3 whitespace-nowrap">{{ formatDate(e.created_at) }}</td>
            <td class="border-b border-border-subtle px-5 py-3">
              <span :class="e.reversed && 'line-through'">{{ e.description }}</span>
              <span v-if="e.reversed" class="ml-2 no-underline"><UiBadge status="vacant" label="Reversed" /></span>
              <span v-else-if="isReversal(e)" class="ml-2"><UiBadge status="review" label="Reversal" /></span>
            </td>
            <td :class="['money border-b border-border-subtle px-5 py-3 text-right', e.reversed && 'line-through']">{{ e.direction === 'DEBIT' ? cur.format(e.amount) : '' }}</td>
            <td :class="['money border-b border-border-subtle px-5 py-3 text-right', e.reversed && 'line-through']">{{ e.direction === 'CREDIT' ? cur.format(e.amount) : '' }}</td>
            <td :class="['money border-b border-border-subtle px-5 py-3 text-right font-semibold', cur.isNegative(e.running_balance) || cur.isZero(e.running_balance) ? 'text-paid-text' : 'text-arrears-text']">{{ cur.format(e.running_balance) }}</td>
            <td class="border-b border-border-subtle px-3">
              <UiButton v-if="canReverse(e)" variant="icon" :label="`Reverse: ${e.description}`" @click="$emit('reverse', e)"><Icon name="lucide:undo-2" class="size-5" /></UiButton>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
