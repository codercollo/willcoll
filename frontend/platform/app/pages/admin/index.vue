<script setup lang="ts">
// The route guard sends /admin to the managers list.
definePageMeta({ layout: false })
await navigateTo('/admin/managers', { replace: true })
</script>

<template><div /></template>
