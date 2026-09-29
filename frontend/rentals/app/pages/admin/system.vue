<script setup lang="ts">
import type { SystemHealth } from '~/types/admin'
import { formatBytes } from '~/utils/format'

definePageMeta({ title: 'System', layout: 'admin' })

const admin = useAdmin()
const h = ref<SystemHealth | null>(null)
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try { h.value = await admin.health() } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load system health.' } finally { loading.value = false }
}
onMounted(load)

const backup = computed(() => {
  const s = h.value?.backups.status
  if (s === 'ok') return { status: 'paid' as const, label: 'Backups healthy' }
  if (s === 'not_configured') return { status: 'pending' as const, label: 'Not configured' }
  return { status: 'arrears' as const, label: s ?? 'Unknown' }
})
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex justify-end"><UiButton variant="secondary" :loading="loading" @click="load"><Icon name="lucide:refresh-cw" class="size-4" />Refresh</UiButton></div>
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading && !h" block />

    <template v-else-if="h">
      <section class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-label="Database">
        <UiKpiTile label="Database" :value="h.database.status === 'ok' ? 'Healthy' : h.database.status" :tone="h.database.status === 'ok' ? 'paid' : 'arrears'" :delta="`${h.database.latency_ms} ms round trip`" />
        <UiKpiTile label="Database size" :value="formatBytes(h.database.size_bytes)" />
        <UiKpiTile label="Connections" :value="String(h.database.connections)" />
        <UiKpiTile label="WAL failures" :value="String(h.backups.wal_archiving.failed_count)" :tone="h.backups.wal_archiving.failed_count ? 'arrears' : 'paid'" :delta="`${h.backups.wal_archiving.archived_count} archived`" />
      </section>

      <UiCard>
        <div class="flex items-center justify-between gap-4"><h2>Backups</h2><UiBadge :status="backup.status" :label="backup.label" /></div>
        <p v-if="h.backups.status === 'not_configured'" class="mt-2 text-gray-700">No base backup or WAL archiving has been reported. Set up backups before going live.</p>
        <dl v-else class="mt-4 grid grid-cols-2 gap-6 md:grid-cols-3">
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Last WAL archived</dt><dd class="font-semibold">{{ h.backups.wal_archiving.last_successful_at ? formatDateTime(h.backups.wal_archiving.last_successful_at) : '—' }}</dd></div>
          <div><dt class="text-[length:var(--text-caption)] text-gray-500">Last failure</dt><dd class="font-semibold">{{ h.backups.wal_archiving.last_failed_at ? formatDateTime(h.backups.wal_archiving.last_failed_at) : 'None' }}</dd></div>
        </dl>
      </UiCard>

      <UiCard :padded="false">
        <h2 class="px-6 pt-6 pb-4">Largest tables</h2>
        <table class="w-full text-left">
          <thead><tr class="h-10 border-y border-border-default bg-gray-50"><th class="th-text px-5 font-medium">Table</th><th class="th-text px-5 text-right font-medium">Size</th></tr></thead>
          <tbody>
            <tr v-for="t in h.database.largest_tables" :key="t.name" class="h-12 border-b border-border-subtle"><td class="px-5 font-semibold">{{ t.name }}</td><td class="tnum px-5 text-right">{{ formatBytes(t.size_bytes) }}</td></tr>
          </tbody>
        </table>
      </UiCard>
    </template>
  </div>
</template>
