<script setup lang="ts">
import type { DatabaseHealth, ServiceStatus, SystemHealth } from '~/types/admin'
import type { BadgeStatus } from '#base/components/ui/Badge.vue'
import { formatBytes } from '#base/utils/format'

definePageMeta({ title: 'System', layout: 'admin' })

// platform-api checks every service itself, each under a short timeout, and
// returns what it got: one service being down shows here as that service
// being down, never as a blank page.
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

const serviceBadge: Record<ServiceStatus, { status: BadgeStatus; label: string }> = {
  ok: { status: 'paid', label: 'Healthy' },
  degraded: { status: 'partial', label: 'Degraded' },
  unreachable: { status: 'arrears', label: 'Unreachable' },
  unauthorized: { status: 'arrears', label: 'Session refused' },
  not_configured: { status: 'pending', label: 'Not configured' },
}

const backupBadge = (s: string | undefined): { status: BadgeStatus; label: string } => {
  if (s === 'healthy') return { status: 'paid', label: 'Backups healthy' }
  if (s === 'not_configured') return { status: 'pending', label: 'Not configured' }
  return { status: 'arrears', label: s ?? 'Unknown' }
}

const rentals = computed(() => h.value?.services.rentals)
const report = computed(() => rentals.value?.report)
const unclaimed = computed(() => unclaimedChannelsSummary(report.value?.unclaimed_channels))
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex items-center justify-between gap-4">
      <p v-if="h" class="text-[length:var(--text-caption)] text-gray-500">Checked {{ formatDateTime(h.checked_at) }} &middot; {{ h.system_info.environment }} &middot; {{ h.system_info.version }}</p>
      <span v-else />
      <UiButton variant="secondary" :loading="loading" @click="load"><Icon name="lucide:refresh-cw" class="size-4" />Refresh</UiButton>
    </div>
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading && !h" block />

    <template v-else-if="h">
      <UiFormAlert v-if="h.status !== 'available'" :tone="h.status === 'degraded' ? 'info' : undefined">
        {{ h.status === 'degraded' ? 'One or more services need attention. The rest are shown below.' : 'The platform database is unreachable.' }}
      </UiFormAlert>

      <!-- Platform -->
      <section class="flex flex-col gap-4" aria-label="Platform">
        <div class="flex items-center gap-3"><h2>Platform</h2><UiBadge v-bind="serviceBadge[h.services.platform.status]" /></div>
        <UiFormAlert v-if="h.services.platform.status !== 'ok'">willcoll_platform: {{ h.services.platform.database.error ?? 'not reachable' }}.</UiFormAlert>
        <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <UiKpiTile label="Database" value="Healthy" tone="paid" :delta="`${h.services.platform.database.latency_ms} ms round trip`" />
          <UiKpiTile label="Database size" :value="formatBytes(h.services.platform.database.size_bytes ?? 0)" />
          <UiKpiTile label="Connections" :value="String(h.services.platform.database.connections ?? 0)" />
          <UiKpiTile label="Audit write failures (24h)" :value="String(h.services.platform.audit_failures_24h ?? 0)" :tone="h.services.platform.audit_failures_24h ? 'arrears' : 'paid'" />
        </div>
      </section>

      <!-- Rentals -->
      <section class="flex flex-col gap-4" aria-label="Rentals">
        <div class="flex items-center gap-3">
          <h2>Rentals</h2><UiBadge v-bind="serviceBadge[rentals!.status]" />
          <span v-if="rentals!.latency_ms !== undefined" class="text-[length:var(--text-caption)] text-gray-500">{{ rentals!.latency_ms }} ms</span>
        </div>
        <UiFormAlert v-if="rentals!.status === 'not_configured'" tone="info">platform-api has no RENTALS_API_URL, so it cannot ask rentals-api for its report.</UiFormAlert>
        <UiFormAlert v-else-if="rentals!.status === 'unauthorized'">rentals-api refused this admin session. Sign out and in again; if it persists, the two APIs are not sharing the session cookie.</UiFormAlert>
        <UiFormAlert v-else-if="!report">rentals-api did not answer ({{ rentals!.error ?? 'no response' }}). Its database and backups cannot be shown.</UiFormAlert>

        <template v-else>
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <UiKpiTile label="Database" :value="report.database.status === 'ok' ? 'Healthy' : report.database.status" :tone="report.database.status === 'ok' ? 'paid' : 'arrears'" :delta="`${report.database.latency_ms} ms round trip`" />
            <UiKpiTile label="Database size" :value="formatBytes((report.database as DatabaseHealth).size_bytes ?? 0)" />
            <UiKpiTile label="Connections" :value="String(report.database.connections ?? 0)" />
            <UiKpiTile label="WAL failures" :value="String(report.backups.wal_archiving.failed_count)" :tone="report.backups.wal_archiving.failed_count ? 'arrears' : 'paid'" :delta="`${report.backups.wal_archiving.archived_count} archived`" />
            <UiKpiTile label="Audit write failures (24h)" :value="String(report.audit_failures_24h)" :tone="report.audit_failures_24h ? 'arrears' : 'paid'" />
            <UiKpiTile label="Inbox fallbacks (24h)" :value="String(report.inbox_fallbacks_24h)" :tone="report.inbox_fallbacks_24h ? 'arrears' : 'paid'" delta="payments posted without platform's inbox" />
            <UiKpiTile label="Usage reporting" :value="report.usage_reporting.enabled ? 'On' : 'Off'" :tone="report.usage_reporting.enabled ? 'paid' : 'arrears'" :delta="report.usage_reporting.last_report_at ? `last ${formatDateTime(report.usage_reporting.last_report_at)}` : 'no report yet'" />
            <UiKpiTile label="Unclaimed channels" :value="String(unclaimed.count)" :tone="unclaimed.count ? 'arrears' : 'paid'" :delta="unclaimed.text" />
          </div>
          <UiFormAlert v-if="unclaimed.count">
            Unclaimed channels: {{ unclaimed.count }} ({{ unclaimed.text }}). Payments on these channels were still posted, but platform has no claim on them. Re-save the channel on the property, or run make dev/setup in development.
          </UiFormAlert>

          <UiCard>
            <div class="flex items-center justify-between gap-4"><h3>Backups</h3><UiBadge v-bind="backupBadge(report.backups.status)" /></div>
            <p v-if="report.backups.status === 'not_configured'" class="mt-2 text-gray-700">No base backup or WAL archiving has been reported. Set up backups before going live.</p>
            <dl v-else class="mt-4 grid grid-cols-2 gap-6 md:grid-cols-3">
              <div><dt class="text-[length:var(--text-caption)] text-gray-500">Last WAL archived</dt><dd class="font-semibold">{{ report.backups.wal_archiving.last_successful_at ? formatDateTime(report.backups.wal_archiving.last_successful_at) : '—' }}</dd></div>
              <div><dt class="text-[length:var(--text-caption)] text-gray-500">Last failure</dt><dd class="font-semibold">{{ report.backups.wal_archiving.last_failed_at ? formatDateTime(report.backups.wal_archiving.last_failed_at) : 'None' }}</dd></div>
              <div><dt class="text-[length:var(--text-caption)] text-gray-500">Last base backup</dt><dd class="font-semibold">{{ report.backups.last_base_backup?.completed_at ? formatDateTime(report.backups.last_base_backup.completed_at) : '—' }}</dd></div>
            </dl>
          </UiCard>

          <UiCard :padded="false">
            <h3 class="px-6 pt-6 pb-4">Largest tables (rentals)</h3>
            <table class="w-full text-left">
              <thead><tr class="h-10 border-y border-border-default bg-gray-50"><th class="th-text px-5 font-medium">Table</th><th class="th-text px-5 text-right font-medium">Size</th></tr></thead>
              <tbody>
                <tr v-for="t in report.database.largest_tables ?? []" :key="t.name" class="h-12 border-b border-border-subtle"><td class="px-5 font-semibold">{{ t.name }}</td><td class="tnum px-5 text-right">{{ formatBytes(t.size_bytes) }}</td></tr>
              </tbody>
            </table>
          </UiCard>
        </template>
      </section>

      <UiCard v-if="h.services.platform.status === 'ok'" :padded="false">
        <h3 class="px-6 pt-6 pb-4">Largest tables (platform)</h3>
        <table class="w-full text-left">
          <thead><tr class="h-10 border-y border-border-default bg-gray-50"><th class="th-text px-5 font-medium">Table</th><th class="th-text px-5 text-right font-medium">Size</th></tr></thead>
          <tbody>
            <tr v-for="t in h.services.platform.database.largest_tables ?? []" :key="t.name" class="h-12 border-b border-border-subtle"><td class="px-5 font-semibold">{{ t.name }}</td><td class="tnum px-5 text-right">{{ formatBytes(t.size_bytes) }}</td></tr>
          </tbody>
        </table>
      </UiCard>
    </template>
  </div>
</template>
