<script setup lang="ts">
// Component gallery: every ui component in every state (Phase 0 exit check).
definePageMeta({ title: 'Design system', layout: 'default' })

const toast = useToast()
const modal = ref(false)
const confirm = ref(false)
const tab = ref('units')
const step = ref(1)
const name = ref('')
const amount = ref('18000.00')
const sel = ref('')
const statuses = ['paid', 'arrears', 'partial', 'vacant', 'pending', 'review'] as const
</script>

<template>
  <div class="space-y-8">
    <section class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <UiKpiTile label="Rent collected" value="Ksh 80,777.00" delta="+12% vs last month" />
      <UiKpiTile label="Arrears" value="Ksh 24,000.00" delta="3 units" tone="arrears" />
      <UiKpiTile label="Units" value="17 / 20" />
      <UiKpiTile label="Awaiting review" value="8" />
    </section>

    <UiCard>
      <h2 class="mb-4">Badges</h2>
      <div class="flex flex-wrap gap-3"><UiBadge v-for="s in statuses" :key="s" :status="s" /></div>
    </UiCard>

    <UiCard>
      <h2 class="mb-4">Buttons</h2>
      <div class="flex flex-wrap items-center gap-3">
        <UiButton>Primary</UiButton>
        <UiButton disabled>Disabled</UiButton>
        <UiButton loading>Saving</UiButton>
        <UiButton variant="secondary">Secondary</UiButton>
        <UiButton variant="destructive">Terminate lease</UiButton>
        <UiButton variant="icon" label="More"><Icon name="lucide:ellipsis" class="size-5" /></UiButton>
      </div>
      <div class="mt-4 max-w-sm"><UiButton block large>Pay now (pay page size)</UiButton></div>
    </UiCard>

    <UiCard>
      <h2 class="mb-6">Form</h2>
      <div class="flex max-w-md flex-col gap-5">
        <UiField label="Tenant name" for="n" help="As on the lease"><UiInput id="n" v-model="name" placeholder="Joseph Kamau" /></UiField>
        <UiField label="Rent" for="a"><UiInput id="a" v-model="amount" money /></UiField>
        <UiField label="Phone" for="p" error="must be a valid Kenyan number"><UiInput id="p" model-value="0722" invalid /></UiField>
        <UiField label="Ledger" for="s"><UiSelect id="s" v-model="sel" placeholder="Choose" :options="[{ value: 'rent', label: 'Rent' }, { value: 'water', label: 'Water' }]" /></UiField>
      </div>
    </UiCard>

    <UiCard>
      <h2 class="mb-4">Tabs</h2>
      <UiTabs v-model="tab" :tabs="[{ key: 'units', label: 'Units' }, { key: 'rent', label: 'Rent' }, { key: 'water', label: 'Water' }, { key: 'reports', label: 'Reports' }]" />
    </UiCard>

    <UiCard>
      <h2 class="mb-6">STK push stepper</h2>
      <UiStepper :steps="['Sent to your phone', 'Waiting for PIN', 'Confirmed']" :current="step" />
      <div class="mt-4 flex gap-3">
        <UiButton variant="secondary" @click="step = Math.max(0, step - 1)">Back</UiButton>
        <UiButton variant="secondary" @click="step = Math.min(2, step + 1)">Next</UiButton>
      </div>
    </UiCard>

    <UiCard :padded="false">
      <UiEmptyState title="No payments yet" text="Payments show here as tenants pay." />
    </UiCard>

    <UiCard><UiSkeleton :lines="3" /></UiCard>

    <div class="flex flex-wrap gap-3">
      <UiButton variant="secondary" @click="modal = true">Open modal</UiButton>
      <UiButton variant="secondary" @click="confirm = true">Open confirm</UiButton>
      <UiButton variant="secondary" @click="toast.success('Saved')">Success toast</UiButton>
      <UiButton variant="secondary" @click="toast.error('Could not save')">Error toast</UiButton>
    </div>

    <UiModal v-model="modal" title="Add a unit">
      <UiField label="Unit code" for="u"><UiInput id="u" v-model="name" /></UiField>
      <template #footer><UiButton variant="secondary" @click="modal = false">Cancel</UiButton><UiButton @click="modal = false">Save</UiButton></template>
    </UiModal>
    <UiConfirmDialog v-model="confirm" title="Terminate lease?" text="This ends the lease today." confirm-label="Terminate" destructive @confirm="confirm = false" />
  </div>
</template>
