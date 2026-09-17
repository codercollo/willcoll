<script setup lang="ts">
interface StatementRow {
  unit_id: string
  period_month: string
  rent_due: string | number
  rent_paid: string | number
  water_due: string | number
  water_paid: string | number
}

const props = defineProps<{ rows: StatementRow[] }>()

function monthLabel(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', year: 'numeric' })
}
function settled(due: string | number, paid: string | number) {
  return Number(paid) >= Number(due)
}
</script>

<template>
  <ol class="ledger-timeline">
    <li v-for="row in props.rows" :key="row.period_month" class="ledger-entry">
      <div class="ledger-entry__period">{{ monthLabel(row.period_month) }}</div>
      <div class="ledger-entry__line">
        <span>Rent</span>
        <span>KES {{ Number(row.rent_paid).toLocaleString() }} / {{ Number(row.rent_due).toLocaleString() }}</span>
        <StatusBadge :label="settled(row.rent_due, row.rent_paid) ? 'Paid' : 'Open'" :tone="settled(row.rent_due, row.rent_paid) ? 'success' : 'warning'" />
      </div>
      <div v-if="Number(row.water_due) > 0" class="ledger-entry__line">
        <span>Water &amp; garbage</span>
        <span>KES {{ Number(row.water_paid).toLocaleString() }} / {{ Number(row.water_due).toLocaleString() }}</span>
        <StatusBadge :label="settled(row.water_due, row.water_paid) ? 'Paid' : 'Open'" :tone="settled(row.water_due, row.water_paid) ? 'success' : 'warning'" />
      </div>
    </li>
  </ol>
</template>

<style scoped>
.ledger-timeline {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--space-3);
}

.ledger-entry {
  padding: var(--space-3) var(--space-4);
  background: var(--surface-panel, #FFFFFF);
  border: var(--border-hairline, 1px solid #E2E5E9);
  border-radius: var(--radius-sm, 4px);
}

.ledger-entry__period {
  font-weight: 600;
  margin-bottom: var(--space-2);
  color: var(--color-text-primary, #1F2320);
}

.ledger-entry__line {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-1) 0;
  color: var(--color-text-muted, #6B7280);
  font-size: var(--text-ui-sm, 0.875rem);
}

.ledger-entry__line span:nth-child(2) {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
  color: var(--color-text-primary, #1F2320);
}
</style>
