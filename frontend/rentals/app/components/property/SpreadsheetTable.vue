<script setup lang="ts" generic="Row extends Record<string, any>">
// The paper ledger, on screen (design-tokens 7.4): flat white card, sticky
// header and totals row, 48px rows, blue-50 hover, locked rows greyed with a
// lock icon. Cells are supplied through slots named cell-<key>.
export interface Column { key: string; label: string; align?: 'left' | 'right'; width?: string }

const props = defineProps<{
  columns: Column[]
  rows: Row[]
  rowKey: (row: Row) => string
  locked?: (row: Row) => boolean
  footer?: Record<string, string> // column key -> total text; first column shows the label
  footerLabel?: string
  empty?: string
  label?: string
}>()

const isLocked = (r: Row) => props.locked?.(r) ?? false
</script>

<template>
  <div class="card overflow-hidden">
    <!-- Focusable so a keyboard user can scroll a wide table (WCAG 2.1.1). -->
    <div class="max-h-[70vh] overflow-auto" role="region" :aria-label="label ?? 'Data table'" tabindex="0">
      <table class="w-full min-w-max border-separate border-spacing-0 text-left">
        <thead>
          <tr class="h-10">
            <th class="sticky top-0 z-10 w-8 border-b border-border-default bg-gray-50" scope="col"><span class="sr-only">Locked</span></th>
            <th
              v-for="c in columns"
              :key="c.key"
              scope="col"
              :style="c.width ? { width: c.width } : undefined"
              :class="['th-text sticky top-0 z-10 border-b border-border-default bg-gray-50 px-5 whitespace-nowrap', c.align === 'right' && 'text-right']"
            >{{ c.label }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="!rows.length"><td :colspan="columns.length + 1" class="px-5 py-12 text-center text-gray-500">{{ empty ?? 'Nothing to show.' }}</td></tr>
          <tr
            v-for="r in rows"
            :key="rowKey(r)"
            :class="['group h-12', isLocked(r) ? 'bg-gray-50 text-gray-700' : 'hover:bg-blue-50']"
          >
            <td class="border-b border-border-subtle pl-3 text-gray-500">
              <Icon v-if="isLocked(r)" name="lucide:lock" class="size-[14px]" aria-label="Locked" />
            </td>
            <td
              v-for="c in columns"
              :key="c.key"
              :class="['border-b border-border-subtle px-5 py-3 whitespace-nowrap', c.align === 'right' && 'money text-right']"
            >
              <slot :name="`cell-${c.key}`" :row="r">{{ (r as any)[c.key] }}</slot>
            </td>
          </tr>
        </tbody>
        <tfoot v-if="footer">
          <tr class="h-12">
            <td class="sticky bottom-0 border-t border-border-default bg-surface-card" />
            <td
              v-for="(c, i) in columns"
              :key="c.key"
              :class="['money sticky bottom-0 border-t border-border-default bg-surface-card px-5 text-[length:var(--text-money-lg)] font-bold', c.align === 'right' && 'text-right']"
            >{{ i === 0 ? (footerLabel ?? 'Totals') : (footer[c.key] ?? '') }}</td>
          </tr>
        </tfoot>
      </table>
    </div>
  </div>
</template>
