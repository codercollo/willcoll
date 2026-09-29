<script setup lang="ts">
import type { Landlord } from '~/types/api'

definePageMeta({ title: 'Landlords' })

const store = useProperties()
const loading = ref(true)
const error = ref('')
const editing = ref<Landlord | null>(null)
const open = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try { await store.fetchLandlords() } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load landlords.' } finally { loading.value = false }
}
onMounted(load)

function edit(l: Landlord | null) { editing.value = l; open.value = true }
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex justify-end"><UiButton @click="edit(null)"><Icon name="lucide:plus" class="size-4" />Add landlord</UiButton></div>
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading" block />
    <UiCard v-else :padded="false">
      <UiEmptyState v-if="!store.landlords.length" title="No landlords yet" text="A landlord owns one or more properties you manage." icon="lucide:users" />
      <div v-else class="overflow-x-auto">
        <table class="w-full text-left">
          <thead>
            <tr class="h-10 border-b border-border-default bg-gray-50">
              <th class="th-text px-5 font-medium">Name</th><th class="th-text px-5 font-medium">Phone</th>
              <th class="th-text px-5 font-medium">Email</th><th class="th-text px-5 font-medium">Bank</th><th class="w-12" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="l in store.landlords" :key="l.id" class="h-12 border-b border-border-subtle hover:bg-blue-50">
              <td class="px-5 font-semibold">{{ l.name }}</td>
              <td class="tnum px-5">{{ l.phone }}</td>
              <td class="px-5 text-gray-700">{{ l.email || '—' }}</td>
              <td class="px-5 text-gray-700">{{ l.bank_name ? `${l.bank_name} · ${l.bank_account_number ?? ''}` : '—' }}</td>
              <td class="px-3"><UiButton variant="icon" :label="`Edit ${l.name}`" @click="edit(l)"><Icon name="lucide:pencil" class="size-5" /></UiButton></td>
            </tr>
          </tbody>
        </table>
      </div>
    </UiCard>
    <LandlordFormModal v-model="open" :landlord="editing" />
  </div>
</template>
