<script setup lang="ts">
// Horizontal steps joined by a 2px line that fills with the accent as it
// progresses (design-tokens 7.8). State is also carried by icon, not motion.
const props = defineProps<{ steps: string[]; current: number; failed?: boolean }>()
</script>

<template>
  <ol class="flex items-start">
    <li v-for="(s, i) in steps" :key="s" class="relative flex flex-1 flex-col items-center gap-2 text-center">
      <span
        v-if="i > 0"
        :class="['absolute top-3 right-1/2 h-0.5 w-full -translate-y-1/2 transition-colors duration-(--duration-slow)', i <= props.current ? 'bg-accent-primary' : 'bg-gray-300']"
        aria-hidden="true"
      />
      <span
        :class="[
          'relative z-10 flex size-6 items-center justify-center rounded-full',
          props.failed && i === props.current ? 'bg-arrears text-white' : i < props.current ? 'bg-accent-primary text-white' : i === props.current ? 'border-2 border-accent-primary bg-white text-accent-text' : 'border-2 border-gray-300 bg-white text-gray-500',
        ]"
      >
        <Icon :name="props.failed && i === props.current ? 'lucide:x' : i < props.current ? 'lucide:check' : 'lucide:circle'" class="size-[14px]" />
      </span>
      <span :class="['text-[length:var(--text-caption)] leading-[var(--text-caption--line-height)]', i <= props.current ? 'font-semibold text-gray-900' : 'text-gray-500']">{{ s }}</span>
    </li>
  </ol>
</template>
