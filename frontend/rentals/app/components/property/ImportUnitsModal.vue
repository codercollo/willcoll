<script setup lang="ts">
import { ApiError } from '~/utils/apiError'
import type { ImportRowError } from '~/types/api'

const open = defineModel<boolean>({ default: false })
const props = defineProps<{ propertyId: string }>()
const emit = defineEmits<{ imported: [] }>()

const units = useUnits()
const toast = useToast()
const file = ref<File | null>(null)
const checking = ref(false)
const importing = ref(false)
const validRows = ref<number | null>(null)
const problems = ref<ImportRowError[]>([])
const general = ref('')

watch(open, (v) => { if (v) reset() })
function reset() { file.value = null; validRows.value = null; problems.value = []; general.value = '' }

function pick(e: Event) {
  reset()
  file.value = (e.target as HTMLInputElement).files?.[0] ?? null
  if (file.value) check()
}

// Step 1: dry run. Nothing is written until the whole sheet is clean.
async function check() {
  if (!file.value) return
  checking.value = true
  try {
    const res = await units.importCsv(props.propertyId, file.value, true)
    validRows.value = res.valid_rows ?? 0
  } catch (e) { fail(e) } finally { checking.value = false }
}

async function doImport() {
  if (!file.value) return
  importing.value = true
  try {
    const res = await units.importCsv(props.propertyId, file.value, false)
    toast.success(`${res.imported ?? 0} units imported`)
    emit('imported')
    open.value = false
  } catch (e) { fail(e) } finally { importing.value = false }
}

function fail(e: unknown) {
  validRows.value = null
  if (e instanceof ApiError && Array.isArray(e.details?.rows)) { problems.value = e.details.rows as ImportRowError[]; general.value = e.message }
  else general.value = e instanceof Error ? e.message : 'The file could not be checked.'
}

const template = 'unit_code,meter_number\nG1,W-001\nG2,W-002\n'
const templateHref = computed(() => `data:text/csv;charset=utf-8,${encodeURIComponent(template)}`)
</script>

<template>
  <UiModal v-model="open" title="Import units from CSV" width="lg">
    <div class="flex flex-col gap-5">
      <p class="text-gray-700">The first row is a header: <code class="rounded-sm bg-gray-100 px-1">unit_code</code> is required, <code class="rounded-sm bg-gray-100 px-1">meter_number</code> is optional. Up to 500 rows. Either every row is imported or none is.
        <a :href="templateHref" download="units-template.csv" class="font-semibold text-accent-text">Download a template</a></p>

      <UiField label="CSV file" for="csv-file">
        <input id="csv-file" type="file" accept=".csv,text/csv" class="block w-full rounded-sm border border-border-default bg-white p-2 file:mr-4 file:rounded-sm file:border-0 file:bg-gray-100 file:px-4 file:py-2 file:font-semibold" @change="pick">
      </UiField>

      <UiSkeleton v-if="checking" :lines="2" />
      <UiFormAlert v-if="validRows !== null" tone="success">The file is valid: {{ validRows }} units will be added, all vacant.</UiFormAlert>

      <template v-if="general">
        <UiFormAlert>{{ general }}</UiFormAlert>
        <div v-if="problems.length" class="max-h-64 overflow-auto rounded-sm border border-border-default">
          <table class="w-full text-left">
            <thead class="sticky top-0 bg-gray-50"><tr class="h-10"><th class="th-text px-4 font-medium">Row</th><th class="th-text px-4 font-medium">Column</th><th class="th-text px-4 font-medium">Problem</th></tr></thead>
            <tbody>
              <tr v-for="(p, i) in problems" :key="i" class="border-t border-border-subtle">
                <td class="tnum px-4 py-2 font-semibold">{{ p.row }}</td><td class="px-4 py-2">{{ p.field }}</td><td class="px-4 py-2">{{ p.message }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="problems.length" class="text-gray-500">Fix these in your sheet and choose the file again. Row numbers match the lines in the file.</p>
      </template>
    </div>
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton :loading="importing" :disabled="validRows === null || checking" @click="doImport">Import {{ validRows ?? '' }} units</UiButton>
    </template>
  </UiModal>
</template>
