<script setup lang="ts">
import { formatUnitCodeInput, isUnitCode, normalizeUnitCode } from '~/utils/unitCode'

// Where a tenant lands without a link: type the code printed under the QR on
// the door. It resolves through the SAME public endpoint the QR uses
// (/q/:code), which redirects to the pay page, or to /pay/inactive.
definePageMeta({ layout: 'public', title: 'Pay rent' })
useHead({ title: 'Pay rent' })

const config = useRuntimeConfig()
const code = ref('')
const error = ref('')
const going = ref(false)

watch(code, (v) => { const f = formatUnitCodeInput(v); if (f !== v) code.value = f; error.value = '' })

function go() {
  if (!isUnitCode(code.value)) {
    error.value = 'That code does not look right. It has 12 letters and numbers, like K7QM-2XH9-PTRB.'
    return
  }
  going.value = true
  window.location.assign(`${config.public.apiBase.replace(/\/$/, '')}/q/${normalizeUnitCode(code.value)}`)
}
</script>

<template>
  <PayCard>
    <h2>Pay your rent</h2>
    <p class="mt-1 text-gray-700">Scan the QR sticker on your door with your phone camera. If it will not scan, type the code printed under it.</p>
    <form class="mt-6 flex flex-col gap-5" novalidate @submit.prevent="go">
      <UiField label="Unit code" for="unit-code" help="Letters and numbers only. Capitals and dashes do not matter." :error="error">
        <UiInput id="unit-code" v-model="code" placeholder="K7QM-2XH9-PTRB" autocomplete="off" inputmode="text" :invalid="!!error" />
      </UiField>
      <UiButton type="submit" block large :loading="going" :disabled="!code">Continue</UiButton>
    </form>
    <p class="mt-6 text-center text-gray-500">You will confirm your phone number by SMS before you can pay.</p>
  </PayCard>
</template>
