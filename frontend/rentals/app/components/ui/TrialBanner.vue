<script setup lang="ts">
// The free-trial countdown, shown to a manager on every dashboard page until
// they subscribe. Amber in the last two days.
const auth = useAuthStore()
const route = useRoute()

const trial = computed(() => (auth.access?.state === 'trial' ? auth.access : null))
const urgent = computed(() => (trial.value?.days_left ?? 99) <= 2)
const onBilling = computed(() => route.path.startsWith('/billing'))
</script>

<template>
  <div
    v-if="trial && !onBilling"
    :class="['mb-6 flex flex-wrap items-center justify-between gap-3 rounded-md px-5 py-3', urgent ? 'bg-partial-tint' : 'bg-accent-primary-tint']"
    role="status"
    data-testid="trial-banner"
  >
    <p class="flex items-center gap-2 text-gray-900">
      <Icon :name="urgent ? 'lucide:hourglass' : 'lucide:sparkles'" class="size-5 shrink-0" />
      <span><strong>Free trial:</strong> {{ trial.days_left }} {{ trial.days_left === 1 ? 'day' : 'days' }} left.<span class="hidden sm:inline"> Everything is unlocked. Choose a plan any time to keep going.</span></span>
    </p>
    <UiButton to="/billing" :variant="urgent ? 'primary' : 'secondary'">Choose a plan</UiButton>
  </div>
</template>
