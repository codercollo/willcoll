<script setup lang="ts">
import { PRODUCTS } from '#base/utils/appLinks'
import { STAFF_ROLES, type StaffInput, type StaffMember } from '~/types/staff'

// Invite a staff member (no `staff`) or edit one: name, phone, role and which
// products they may open. The email is their sign-in and is fixed once invited.
const open = defineModel<boolean>({ default: false })
const props = defineProps<{ staff?: StaffMember | null }>()
const emit = defineEmits<{ saved: [StaffMember] }>()

const store = useStaff()
const toast = useToast()
const { loading, fields, message, run } = useSubmit()
const form = reactive({ name: '', email: '', phone: '', role: 'viewer', access: [] as string[] })
const editing = computed(() => !!props.staff)
const roleHelp = computed(() => STAFF_ROLES.find(r => r.value === form.role)?.help)

watch(open, (v) => {
  if (!v) return
  const s = props.staff
  Object.assign(form, { name: s?.name ?? '', email: s?.email ?? '', phone: s?.phone ?? '', role: s?.role ?? 'viewer', access: [...(s?.module_access ?? ['rentals'])] })
  fields.value = {}
  message.value = ''
})

function toggle(key: string, on: boolean) {
  form.access = on ? [...new Set([...form.access, key])] : form.access.filter(k => k !== key)
}

async function save() {
  const body: StaffInput = { name: form.name.trim(), phone: form.phone.trim() || undefined, role: form.role, module_access: form.access }
  const saved = await run(() => (props.staff ? store.update(props.staff.id, body) : store.invite({ ...body, email: form.email.trim() })))
  if (!saved) return
  toast.success(editing.value ? 'Staff member updated.' : `Invite sent to ${saved.email}.`)
  open.value = false
  emit('saved', saved)
}
</script>

<template>
  <UiModal v-model="open" :title="editing ? 'Edit staff member' : 'Invite staff member'">
    <form class="flex flex-col gap-5" novalidate @submit.prevent="save">
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Name" for="staff-name" :error="fields.name"><UiInput id="staff-name" v-model="form.name" autocomplete="off" :invalid="!!fields.name" /></UiField>
      <UiField label="Email" for="staff-email" :help="editing ? 'Their sign-in. It cannot be changed.' : 'We email them an invite to set a password.'" :error="fields.email"><UiInput id="staff-email" v-model="form.email" type="email" inputmode="email" :disabled="editing" :invalid="!!fields.email" /></UiField>
      <UiField label="Phone" for="staff-phone" optional :error="fields.phone"><UiInput id="staff-phone" v-model="form.phone" type="tel" :invalid="!!fields.phone" /></UiField>
      <UiField label="Role" for="staff-role" :help="roleHelp" :error="fields.role"><UiSelect id="staff-role" v-model="form.role" :options="STAFF_ROLES.map(r => ({ value: r.value, label: r.label }))" :invalid="!!fields.role" /></UiField>
      <fieldset>
        <legend class="mb-2 text-[length:var(--text-label)] font-medium">Products they can open</legend>
        <label v-for="p in PRODUCTS" :key="p.key" class="flex items-center gap-3 py-1" :class="p.available ? '' : 'text-gray-500'">
          <input type="checkbox" class="size-4" :checked="form.access.includes(p.key)" :disabled="!p.available" @change="toggle(p.key, ($event.target as HTMLInputElement).checked)">
          <span>{{ p.label }}</span><UiBadge v-if="!p.available" status="vacant" label="Coming soon" />
        </label>
        <p v-if="fields.module_access" class="mt-1 text-[length:var(--text-caption)] text-arrears-text" role="alert">{{ fields.module_access }}</p>
      </fieldset>
    </form>
    <template #footer>
      <UiButton variant="secondary" @click="open = false">Cancel</UiButton>
      <UiButton :loading="loading" :disabled="!form.name.trim() || (!editing && !form.email.trim())" @click="save">{{ editing ? 'Save changes' : 'Send invite' }}</UiButton>
    </template>
  </UiModal>
</template>
