<script setup lang="ts">
// The password box behind step-up (docs/security/authorization.md, D5):
// shown by useApi when an action needs a recent password re-entry. Mounted
// once per app, next to the toast host.
const step = useStepUp();
const api = useApi();
const { loading, fields, message, run } = useSubmit();
const password = ref("");

// Dismissed (Esc, backdrop, Cancel): release the waiting request unanswered.
// After a success done() has already run and this is a no-op.
watch(step.open, (isOpen) => {
  if (isOpen) return;
  password.value = "";
  fields.value = {};
  message.value = "";
  step.done(false);
});

async function submit() {
  const ok = await run(async () => {
    await api.post("/sessions/step-up", { password: password.value });
    return true;
  });
  if (ok) {
    password.value = "";
    step.done(true);
  }
}
</script>

<template>
  <UiModal v-model="step.open.value" title="Confirm it's you" width="sm">
    <form class="space-y-4" @submit.prevent="submit">
      <p class="text-gray-700">
        This action moves money or changes who can. Enter your password to continue; you won't be asked again for five minutes.
      </p>
      <UiFormAlert v-if="message">{{ message }}</UiFormAlert>
      <UiField label="Password" for="step-up-password" :error="fields.password">
        <UiInput
          id="step-up-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          :invalid="!!fields.password"
        />
      </UiField>
      <button type="submit" class="hidden" tabindex="-1" aria-hidden="true" />
    </form>
    <template #footer>
      <UiButton variant="secondary" @click="step.open.value = false">Cancel</UiButton>
      <UiButton :loading="loading" :disabled="!password" @click="submit">Continue</UiButton>
    </template>
  </UiModal>
</template>
