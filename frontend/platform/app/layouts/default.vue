<script setup lang="ts">
import { managerNav } from '../utils/nav'

const auth = useAuthStore()
const nav = computed(() => managerNav(auth.isOwner))
// A staff member is named; the firm's own login shows the firm.
const who = computed(() => auth.staff?.name ?? auth.manager?.firm_name)
const sub = computed(() => (auth.staff ? `${auth.staff.role} · ${auth.manager?.firm_name ?? ''}` : auth.manager?.email))
</script>

<template>
  <UiAppShell brand="Willcoll" :nav="nav" :user-name="who" :user-sub="sub"><slot /></UiAppShell>
</template>
