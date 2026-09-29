<script setup lang="ts">
type Variant = 'primary' | 'secondary' | 'destructive' | 'icon'
const props = withDefaults(defineProps<{
  variant?: Variant
  block?: boolean
  large?: boolean // 48px thumb target (pay page)
  loading?: boolean
  disabled?: boolean
  type?: 'button' | 'submit'
  to?: string
  label?: string // aria-label for icon buttons
}>(), { variant: 'primary', type: 'button' })

const classes = computed(() => [
  'inline-flex items-center justify-center gap-2 rounded-sm font-semibold transition-colors duration-(--duration-fast) disabled:cursor-not-allowed select-none',
  props.variant === 'icon' ? 'size-8 text-gray-500 hover:bg-gray-100' : props.large ? 'h-12 px-5' : 'h-10 px-5',
  props.block && 'w-full',
  {
    primary: 'bg-accent-primary text-white hover:bg-accent-primary-hover disabled:bg-gray-300 disabled:text-gray-500',
    secondary: 'bg-white text-gray-900 border border-border-default hover:bg-gray-50 disabled:text-gray-500 disabled:bg-gray-50',
    destructive: 'bg-white text-arrears-text border border-arrears/30 hover:bg-arrears-tint disabled:text-gray-500',
    icon: '',
  }[props.variant],
])
</script>

<template>
  <NuxtLink v-if="to && !disabled" :to="to" :class="classes"><slot /></NuxtLink>
  <button v-else :type="type" :class="classes" :disabled="disabled || loading" :aria-label="label" :aria-busy="loading || undefined">
    <Icon v-if="loading" name="lucide:loader-circle" class="size-4 animate-spin" />
    <slot />
  </button>
</template>
