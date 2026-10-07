<script setup lang="ts">
import type { BadgeStatus } from '#base/components/ui/Badge.vue'
import type { StaffMember } from '~/types/staff'

// Staff (owner only): who works under the firm's login, their role and which
// products they may open. Disabling signs them out on every device at once.
definePageMeta({ title: 'Staff' })

const store = useStaff()
const toast = useToast()
const rows = ref<StaffMember[]>([])
const loading = ref(true)
const error = ref('')
const formOpen = ref(false)
const editing = ref<StaffMember | null>(null)

async function load() {
  loading.value = true
  error.value = ''
  try { rows.value = await store.list() } catch (e) { error.value = e instanceof Error ? e.message : 'Could not load staff.' } finally { loading.value = false }
}
onMounted(load)

function openForm(s: StaffMember | null) { editing.value = s; formOpen.value = true }

const statusBadge: Record<StaffMember['status'], { status: BadgeStatus; label: string }> = {
  active: { status: 'paid', label: 'Active' },
  invited: { status: 'pending', label: 'Invited' },
  disabled: { status: 'vacant', label: 'Disabled' },
}

// Disable asks first (it ends their sessions); enabling does not.
const disableTarget = ref<StaffMember | null>(null)
const working = ref(false)
async function setStatus(s: StaffMember, status: 'active' | 'disabled') {
  working.value = true
  try {
    const updated = await store.setStatus(s.id, status)
    rows.value = rows.value.map(r => (r.id === updated.id ? updated : r))
    toast.success(status === 'disabled' ? `${s.name} disabled and signed out.` : `${s.name} enabled.`)
    disableTarget.value = null
  } catch (e) { toast.fail(e) } finally { working.value = false }
}
function saved(s: StaffMember) {
  rows.value = rows.value.some(r => r.id === s.id) ? rows.value.map(r => (r.id === s.id ? s : r)) : [...rows.value, s]
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <p class="text-gray-500">People who sign in under your firm. Choose what each can do and which products they can open.</p>
      <UiButton @click="openForm(null)"><Icon name="lucide:user-plus" class="size-4" />Invite staff</UiButton>
    </div>
    <UiFormAlert v-if="error">{{ error }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading && !rows.length" block />
    <UiCard v-else-if="!error" :padded="false">
      <UiEmptyState v-if="!rows.length" title="No staff yet" text="Invite someone to help run your firm." icon="lucide:users" />
      <div v-else class="overflow-x-auto">
        <table class="stack-table w-full text-left">
          <thead>
            <tr class="h-10 border-b border-border-default bg-gray-50">
              <th class="th-text px-5 font-medium">Name</th><th class="th-text px-5 font-medium">Role</th><th class="th-text px-5 font-medium">Products</th><th class="th-text px-5 font-medium">Status</th><th class="w-24" />
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in rows" :key="s.id" class="h-14 border-b border-border-subtle hover:bg-blue-50">
              <td class="px-5" data-label="Name" data-wide><p class="font-semibold">{{ s.name }}</p><p class="text-[length:var(--text-caption)] text-gray-500">{{ s.email }}</p></td>
              <td class="px-5 capitalize" data-label="Role">{{ s.role }}</td>
              <td class="px-5 capitalize" data-label="Products">{{ s.module_access.length ? s.module_access.join(', ') : 'None' }}</td>
              <td class="px-5" data-label="Status"><UiBadge v-bind="statusBadge[s.status]" /></td>
              <td class="flex items-center gap-1 px-3">
                <UiButton variant="icon" :label="`Edit ${s.name}`" @click="openForm(s)"><Icon name="lucide:pencil" class="size-5" /></UiButton>
                <UiButton v-if="s.status === 'disabled'" variant="icon" :label="`Enable ${s.name}`" @click="setStatus(s, 'active')"><Icon name="lucide:rotate-ccw" class="size-5" /></UiButton>
                <UiButton v-else variant="icon" :label="`Disable ${s.name}`" @click="disableTarget = s"><Icon name="lucide:ban" class="size-5" /></UiButton>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </UiCard>

    <StaffFormModal v-model="formOpen" :staff="editing" @saved="saved" />

    <UiConfirmDialog
      :model-value="!!disableTarget"
      title="Disable this staff member?"
      confirm-label="Disable"
      destructive
      :loading="working"
      :text="`${disableTarget?.name} is signed out on every device immediately and cannot sign in until you enable them again.`"
      @update:model-value="(v: boolean) => { if (!v) disableTarget = null }"
      @confirm="disableTarget && setStatus(disableTarget, 'disabled')"
    />
  </div>
</template>
