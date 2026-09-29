<script setup lang="ts">
import type { Property } from '~/types/api'
import { isValidMoneyInput } from '~/utils/money'

const props = defineProps<{ property: Property }>()
const emit = defineEmits<{ reload: [] }>()

const store = useProperties()
const toast = useToast()

type ChannelType = 'bank' | 'paybill' | 'till'
type CardStatus = 'missing' | 'active' | 'inactive'

const status = ref<CardStatus>(props.property.payhero_channel_id ? 'active' : 'missing')
const channelInfo = ref<{ id: number; channel_type: string; account_number: string } | null>(null)
const checking = ref(false)

function maskAccount(n?: string) {
  if (!n) return ''
  return n.length <= 6 ? n : n.slice(0, 4) + '****' + n.slice(-2)
}

async function checkConnection() {
  checking.value = true
  try {
    const r = await store.paymentChannelStatus(props.property.id)
    status.value = r.status
    channelInfo.value = r.channel ?? null
  } catch (e) { toast.fail(e) } finally { checking.value = false }
}
onMounted(() => { if (props.property.payhero_channel_id) checkConnection() })
watch(() => props.property.id, () => { status.value = props.property.payhero_channel_id ? 'active' : 'missing'; channelInfo.value = null; if (props.property.payhero_channel_id) checkConnection() })

// ---- step 1 + 2: choose destination, fill fields --------------------------
const type = ref<ChannelType>('bank')
const banks = ref<{ name: string; paybill: number }[]>([])
onMounted(() => { store.listPaymentChannelBanks().then(b => { banks.value = b }).catch(() => {}) })

const bank = ref('')
const shortCode = ref('')
const accountNumber = ref('')
const description = ref('')

const hints: Record<ChannelType, string> = {
  bank: "The landlord's bank account number. We'll use the bank's own M-Pesa paybill.",
  paybill: 'A business paybill number, e.g. a SACCO or the landlord\'s own paybill.',
  till: 'A Buy Goods till number.',
}

const connect = useSubmit()
const canSubmit = computed(() => {
  if (!description.value.trim()) return false
  if (type.value === 'bank') return !!bank.value && !!accountNumber.value.trim()
  return !!shortCode.value && /^\d+$/.test(shortCode.value)
})

async function doConnect() {
  if (!canSubmit.value) return
  const input = {
    type: type.value,
    description: description.value.trim(),
    ...(type.value === 'bank'
      ? { bank: bank.value, account_number: accountNumber.value.trim() }
      : { short_code: Number(shortCode.value), account_number: accountNumber.value.trim() || undefined }),
  }
  const r = await connect.run(() => store.registerPaymentChannel(props.property.id, input))
  if (r) {
    status.value = 'active'
    channelInfo.value = { id: r.channel.id, channel_type: type.value, account_number: r.channel.account_number }
    toast.success(r.channel.reused ? 'Connected to your existing channel.' : 'Connected.')
    emit('reload')
  }
}

// ---- KES 10 test ------------------------------------------------------------
const testPhone = ref('')
const testing = useSubmit()
const testResult = ref('')
async function sendTest() {
  testResult.value = ''
  const r = await testing.run(() => store.sendPaymentChannelTest(props.property.id, testPhone.value))
  if (r) testResult.value = r.message
}

// ---- advanced: manual channel id ------------------------------------------
const showManual = ref(false)
const manualId = ref('')
const manualError = computed(() => (manualId.value && !/^[1-9]\d*$/.test(manualId.value) ? 'Must be a positive whole number' : ''))
const manual = useSubmit()
async function saveManual() {
  if (manualError.value || !manualId.value) return
  const r = await manual.run(() => store.update(props.property.id, { payhero_channel_id: manualId.value }))
  if (r) { status.value = 'active'; channelInfo.value = null; showManual.value = false; toast.success('Channel ID saved.'); emit('reload'); checkConnection() }
}
</script>

<template>
  <UiCard>
    <h2 class="mb-1">Get paid</h2>
    <p class="mb-6 text-gray-500">Tenants pay by M-Pesa. The money goes straight to the landlord's bank or paybill; Willcoll never holds it. Tell us where the landlord wants to be paid.</p>

    <div class="mb-6">
      <UiBadge v-if="status === 'missing'" status="pending" label="Not connected" />
      <UiBadge v-else-if="status === 'active'" status="paid" label="Connected" />
      <UiBadge v-else status="arrears" label="Problem" />
      <p v-if="status === 'missing'" class="mt-2 text-[length:var(--text-caption)] text-gray-500">Tenants can't pay online yet.</p>
      <p v-else-if="status === 'inactive'" class="mt-2 text-[length:var(--text-caption)] text-gray-500">PayHero reports this channel isn't active. Check the account it settles to, or connect a different one below.</p>
      <p v-else-if="channelInfo" class="mt-2 text-[length:var(--text-caption)] text-gray-500">Channel #{{ channelInfo.id }} &middot; {{ maskAccount(channelInfo.account_number) || property.payhero_channel_id }}</p>
    </div>

    <template v-if="status !== 'active'">
      <div class="flex flex-col gap-5">
        <div>
          <p class="th-text mb-2">1. Where should the landlord be paid?</p>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <button v-for="opt in (['bank', 'paybill', 'till'] as ChannelType[])" :key="opt" type="button"
              class="rounded-sm border p-4 text-left transition-colors"
              :class="type === opt ? 'border-accent-primary bg-accent-primary-tint' : 'border-border-default bg-white hover:border-accent-primary'"
              @click="type = opt">
              <p class="font-semibold capitalize">{{ opt === 'bank' ? 'Bank account' : opt }}</p>
              <p class="mt-1 text-[length:var(--text-caption)] text-gray-500">{{ hints[opt] }}</p>
            </button>
          </div>
        </div>

        <div class="grid gap-5 sm:grid-cols-2">
          <UiFormAlert v-if="connect.message.value" class="sm:col-span-2">{{ connect.message.value }}</UiFormAlert>

          <UiField v-if="type === 'bank'" label="Bank" for="gp-bank" :error="connect.fields.value.bank" class="sm:col-span-2">
            <UiSelect id="gp-bank" v-model="bank" placeholder="Choose a bank" :options="banks.map(b => ({ value: b.name, label: b.name }))" :invalid="!!connect.fields.value.bank" />
          </UiField>
          <UiField v-if="type === 'bank'" label="Landlord's bank account number" for="gp-acct" help="e.g. 1322334437" :error="connect.fields.value.account_number">
            <UiInput id="gp-acct" v-model="accountNumber" :invalid="!!connect.fields.value.account_number" />
          </UiField>

          <UiField v-if="type !== 'bank'" :label="type === 'paybill' ? 'Paybill number' : 'Till number'" for="gp-code" help="Digits only" :error="connect.fields.value.short_code">
            <UiInput id="gp-code" v-model="shortCode" inputmode="numeric" :invalid="!!connect.fields.value.short_code" />
          </UiField>
          <UiField v-if="type !== 'bank'" label="Account number" for="gp-acct2" optional help="If the paybill needs one" :error="connect.fields.value.account_number">
            <UiInput id="gp-acct2" v-model="accountNumber" :invalid="!!connect.fields.value.account_number" />
          </UiField>

          <UiField label="Name on this account" for="gp-desc" help="e.g. &quot;Mwangi Family Trust — KCB&quot;" :error="connect.fields.value.description" class="sm:col-span-2">
            <UiInput id="gp-desc" v-model="description" :invalid="!!connect.fields.value.description" />
          </UiField>
        </div>

        <div><UiButton :loading="connect.loading.value" :disabled="!canSubmit" @click="doConnect">Connect</UiButton></div>
      </div>
    </template>

    <template v-else>
      <div class="flex flex-wrap items-center gap-3">
        <UiButton variant="secondary" :loading="checking" @click="checkConnection">Check connection</UiButton>
      </div>

      <div class="mt-6 max-w-sm">
        <UiField label="Send a KES 10 test to my phone" for="gp-test-phone" help="Editable — defaults to nothing, enter any Kenyan number">
          <UiInput id="gp-test-phone" v-model="testPhone" type="tel" placeholder="+254712345678" />
        </UiField>
        <UiFormAlert v-if="testing.message.value" tone="error" class="mt-2">{{ testing.message.value }}</UiFormAlert>
        <UiFormAlert v-if="testResult" tone="success" class="mt-2">{{ testResult }}</UiFormAlert>
        <UiButton class="mt-3" variant="secondary" :loading="testing.loading.value" :disabled="!testPhone.trim()" @click="sendTest">Send test</UiButton>
      </div>
    </template>

    <div class="mt-6 border-t border-border-default pt-4">
      <button type="button" class="text-[length:var(--text-caption)] font-semibold text-accent-text" @click="showManual = !showManual">
        Advanced: enter a channel ID manually
      </button>
      <div v-if="showManual" class="mt-3 flex max-w-xs flex-col gap-3">
        <UiFormAlert v-if="manual.message.value" tone="error">{{ manual.message.value }}</UiFormAlert>
        <UiField label="PayHero channel ID" for="gp-manual" :error="manualError || manual.fields.value.payhero_channel_id">
          <UiInput id="gp-manual" v-model="manualId" inputmode="numeric" :invalid="!!manualError" />
        </UiField>
        <UiButton variant="secondary" :loading="manual.loading.value" :disabled="!!manualError || !manualId" @click="saveManual">Save channel ID</UiButton>
      </div>
    </div>
  </UiCard>
</template>
