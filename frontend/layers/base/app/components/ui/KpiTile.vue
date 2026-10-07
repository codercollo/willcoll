<script setup lang="ts">
defineProps<{ label: string; value: string; delta?: string; tone?: 'paid' | 'arrears' }>()
</script>

<template>
  <div class="card flex min-h-[88px] flex-col justify-center p-5">
    <p class="text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)] text-gray-500">{{ label }}</p>
    <p class="money text-[length:var(--text-money-lg)] leading-[var(--text-money-lg--line-height)] font-bold">{{ value }}</p>
    <p v-if="delta" :class="['text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)]', tone === 'arrears' ? 'text-arrears-text' : 'text-paid-text']">{{ delta }}</p>
  </div>
</template>
