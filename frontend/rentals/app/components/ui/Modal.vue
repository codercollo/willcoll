<script setup lang="ts">
const open = defineModel<boolean>({ default: false })
const props = withDefaults(defineProps<{ title?: string; width?: 'sm' | 'md' | 'lg'; dismissible?: boolean }>(), { width: 'md', dismissible: true })

const panel = ref<HTMLElement>()
const titleId = useId()
const widths = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-3xl' }

function close() { if (props.dismissible) open.value = false }

let previous: Element | null = null
watch(open, async (v) => {
  if (!import.meta.client) return
  if (v) {
    previous = document.activeElement
    document.body.style.overflow = 'hidden'
    await nextTick()
    panel.value?.focus()
  } else {
    document.body.style.overflow = ''
    ;(previous as HTMLElement | null)?.focus?.()
  }
})
onBeforeUnmount(() => { if (import.meta.client) document.body.style.overflow = '' })
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-(--duration-slow) ease-standard"
      leave-active-class="transition duration-(--duration-slow) ease-standard"
      enter-from-class="opacity-0"
      leave-to-class="opacity-0"
    >
      <div v-if="open" class="fixed inset-0 z-50 flex items-end justify-center bg-navy-950/50 p-4 sm:items-center" @mousedown.self="close" @keydown.esc="close">
        <div
          ref="panel"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="title ? titleId : undefined"
          tabindex="-1"
          :class="['max-h-[90vh] w-full overflow-y-auto rounded-md bg-white p-8 shadow-modal outline-none', widths[width]]"
        >
          <div v-if="title" class="mb-6 flex items-start justify-between gap-4">
            <h2 :id="titleId">{{ title }}</h2>
            <UiButton v-if="dismissible" variant="icon" label="Close" @click="close"><Icon name="lucide:x" class="size-5" /></UiButton>
          </div>
          <slot />
          <div v-if="$slots.footer" class="mt-8 flex flex-wrap justify-end gap-3"><slot name="footer" /></div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
