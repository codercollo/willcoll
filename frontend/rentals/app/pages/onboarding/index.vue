<script setup lang="ts">
import type { OnboardingStep } from '~/types/onboarding'

// The setup guide: what a new firm does, in order, to go live. Every step is
// ticked from what the firm has really done, so it can never drift.
definePageMeta({ title: 'Setup guide' })

const store = useOnboarding()
const properties = useProperties()
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    await Promise.all([store.fetchStatus(), properties.list.length ? Promise.resolve() : properties.fetchList()])
  } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load the setup guide.' } finally { loading.value = false }
}
onMounted(load)

const status = computed(() => store.status)
const first = computed(() => properties.list[0]?.id)
const percent = computed(() => (status.value?.total ? Math.round((status.value.done / status.value.total) * 100) : 0))

interface Action { label: string; to: string; secondary?: { label: string; to: string } }
function action(step: OnboardingStep): Action {
  const p = first.value
  switch (step.key) {
    case 'landlord': return { label: 'Add a landlord', to: '/landlords' }
    case 'property': return { label: 'Add a property', to: '/properties' }
    case 'tenants': return { label: 'Upload a spreadsheet', to: '/onboarding/import', secondary: { label: 'Add them one by one', to: p ? `/properties/${p}` : '/properties' } }
    case 'balances': return { label: 'Upload with balances', to: '/onboarding/import' }
    case 'water': return { label: 'Open the water grid', to: p ? `/properties/${p}?tab=water` : '/properties' }
    case 'mpesa': return { label: 'Set the PayHero channel', to: p ? `/properties/${p}?tab=settings` : '/properties' }
    case 'billing': return { label: 'Generate rent', to: p ? `/properties/${p}?tab=rent` : '/properties' }
    case 'stickers': return { label: 'Open a unit', to: p ? `/properties/${p}` : '/properties' }
  }
}
</script>

<template>
  <div class="mx-auto flex max-w-3xl flex-col gap-6">
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading && !status" block />

    <template v-else-if="status">
      <UiCard>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2>{{ status.complete ? 'You are live' : 'Let us get you set up' }}</h2>
            <p class="text-gray-700">{{ status.complete ? 'Everything needed to bill and collect is in place.' : `${status.done} of ${status.total} essential steps done. Follow them in order; each takes a few minutes.` }}</p>
          </div>
          <UiButton v-if="!status.complete" :to="action(status.steps.find(s => s.key === status!.next)!).to">Continue</UiButton>
          <UiButton v-else to="/properties">Go to properties</UiButton>
        </div>
        <div class="mt-4 h-2 rounded-full bg-gray-100" role="progressbar" :aria-valuenow="percent" aria-valuemin="0" aria-valuemax="100" aria-label="Setup progress">
          <div class="h-2 rounded-full bg-accent-primary transition-[width] duration-(--duration-slow)" :style="{ width: `${percent}%` }" />
        </div>
      </UiCard>

      <ol class="flex flex-col gap-3">
        <li v-for="(s, i) in status.steps" :key="s.key">
          <UiCard compact :class="[s.key === status.next && 'ring-2 ring-accent-primary']">
            <div class="flex items-start gap-4">
              <span :class="['flex size-8 shrink-0 items-center justify-center rounded-full font-semibold', s.done ? 'bg-paid-tint text-paid-text' : 'bg-gray-100 text-gray-700']" :aria-label="s.done ? 'Done' : `Step ${i + 1}`">
                <Icon v-if="s.done" name="lucide:check" class="size-4" /><template v-else>{{ i + 1 }}</template>
              </span>
              <div class="min-w-0 flex-1">
                <h3 class="flex flex-wrap items-center gap-2">{{ s.title }}<UiBadge v-if="!s.required" status="vacant" label="Optional" /></h3>
                <p class="mt-1 text-gray-700">{{ s.detail }}</p>
                <div v-if="!s.done" class="mt-3 flex flex-wrap gap-3">
                  <UiButton :variant="s.key === status.next ? 'primary' : 'secondary'" :to="action(s).to">{{ action(s).label }}</UiButton>
                  <UiButton v-if="action(s).secondary" variant="secondary" :to="action(s).secondary!.to">{{ action(s).secondary!.label }}</UiButton>
                </div>
              </div>
            </div>
          </UiCard>
        </li>
      </ol>
    </template>
  </div>
</template>
