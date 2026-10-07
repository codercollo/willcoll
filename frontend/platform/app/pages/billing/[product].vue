<script setup lang="ts">
import { ApiError } from '#base/utils/apiError'
import type { PageMeta } from '#base/components/ui/Pagination.vue'
import type { BadgeStatus } from '#base/components/ui/Badge.vue'
import type { Access } from '#base/types/session'
import { PRODUCTS } from '#base/utils/appLinks'

// Billing for ONE product (owner only): its plan, the M-Pesa renewal and its
// invoices. The firm holds an independent subscription per product, so every
// call here is platform-api /v1/billing/<product>/... (utils/apiTarget.ts leaves
// the product segment alone because platform itself has no product).
definePageMeta({ title: 'Billing' })

const route = useRoute()
const product = computed(() => String(route.params.product))
const productInfo = computed(() => PRODUCTS.find(p => p.key === product.value))
const base = computed(() => `/billing/${product.value}`)

interface Plan {
  id: string
  name: string
  price: string
  billing_interval: string
  unit_cap?: number | null
  is_test: boolean
  pricing_type: 'flat' | 'per_unit'
  per_unit_price?: string | null
  min_price?: string | null
  sort_order: number
  computed_price: string
}
interface Subscription { id: string; plan_id: string; status: 'trialing' | 'active' | 'past_due' | 'cancelled'; current_period_start: string; current_period_end: string }
interface Invoice { id: string; amount: string; period: string; status: 'pending' | 'paid' | 'failed'; created_at: string }

const api = useApi()
const auth = useAuthStore()
const toast = useToast()
const cur = useCurrency()

// This product's standing (trial / subscribed / expired): the session read with ?product=.
const access = ref<Access | null>(null)
async function loadAccess() {
  try { access.value = (await api.get<{ access?: Access | null }>('/sessions', { product: product.value })).access ?? null } catch { access.value = null }
}

const loading = ref(true)
const sub = ref<{ subscription: Subscription; plan: Plan } | null>(null)
const plans = ref<Plan[]>([])
const unitCount = ref(0)
const invoices = ref<Invoice[]>([])
const meta = ref<Partial<PageMeta> | null>(null)
const loadError = ref('')

/** A plan whose unit cap is below the firm's current occupied+vacant units:
 * the backend would refuse it (422 "plan_too_small"), so the picker disables
 * it up front with the same reason. */
function tooSmall(p: Plan) {
  return typeof p.unit_cap === 'number' && p.unit_cap < unitCount.value
}

async function loadSubscription() {
  try {
    const res = await api.get<{ subscription: { subscription: Subscription; plan: Plan } }>(`${base.value}/subscription`)
    sub.value = res.subscription
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) sub.value = null // never subscribed
    else throw e
  }
}
async function loadInvoices(page = 1) {
  const res = await api.get<{ invoices: Invoice[]; metadata: Partial<PageMeta> }>(`${base.value}/invoices`, { page, page_size: 10 })
  invoices.value = res.invoices ?? []
  meta.value = res.metadata
}
async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const [p] = await Promise.all([api.get<{ plans: Plan[]; unit_count: number }>(`${base.value}/plans`), loadSubscription(), loadInvoices(), loadAccess()])
    plans.value = p.plans ?? []
    unitCount.value = p.unit_count ?? 0
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : 'Could not load billing.'
  } finally {
    loading.value = false
  }
  // Preselect the current plan, if any, once both are loaded.
  if (sub.value && !chosenPlan.value) chosenPlan.value = sub.value.plan.id
}
onMounted(load)
watch(product, () => { sub.value = null; chosenPlan.value = ''; load() })

// The badge always reflects access (state derived from current_period_end vs
// now, internal/platform/data/access.go), never subscription.status directly: a subscription
// can sit at status "active" with a period that has already lapsed.
const subStatus = computed<{ status: BadgeStatus; label: string }>(() => {
  const state = access.value?.state
  if (state === 'trial') return { status: 'info', label: 'Trial' }
  if (state === 'expired') return { status: 'arrears', label: 'Expired' }
  if (state === 'subscribed') return { status: 'paid', label: 'Active' }
  // The standing could not be read: Fall back to the raw status.
  const s = sub.value?.subscription.status
  if (s === 'past_due') return { status: 'arrears', label: 'Past due' }
  if (s === 'cancelled') return { status: 'vacant', label: 'Cancelled' }
  if (s) return { status: 'paid', label: 'Active' }
  return { status: 'vacant', label: 'No plan' }
})
const lapsed = computed(() => {
  if (access.value) return access.value.state !== 'subscribed'
  return !sub.value || ['past_due', 'cancelled'].includes(sub.value.subscription.status)
})

// Renewal: the M-Pesa prompt goes to the STK phone (the firm phone by
// default, editable in case they want to pay from a different line); poll
// until it settles.
const renew = useSubmit()
const chosenPlan = ref('')
const chosenPlanDetails = computed(() => plans.value.find(p => p.id === chosenPlan.value) ?? null)
/** The backend computes computed_price (flat price, or the per-unit amount
 * for this manager's current unit count); the button shows it directly. */
function payButtonLabel(p: Plan) {
  const amount = `Pay Ksh ${cur.format(p.computed_price)} with M-Pesa`
  return p.pricing_type === 'per_unit' ? `${amount} (${unitCount.value} units)` : amount
}
const stkPhone = ref(auth.manager?.phone ?? '')
watch(() => auth.manager?.phone, (phone) => { if (phone && !stkPhone.value) stkPhone.value = phone })
const paying = ref<'idle' | 'waiting' | 'paid' | 'failed'>('idle')
const payingInvoice = ref('')
let timer: ReturnType<typeof setInterval> | undefined
let ticks = 0

async function startRenewal() {
  if (!chosenPlan.value) return
  const body = { plan_id: chosenPlan.value, phone: stkPhone.value.trim() }
  const res = await renew.run(() => api.post<{ invoice: Invoice; message: string }>(`${base.value}/subscription/renew`, body))
  if (!res) return
  toast.info(res.message || 'Check your phone for the M-Pesa prompt.')
  payingInvoice.value = res.invoice.id
  paying.value = 'waiting'
  ticks = 0
  timer = setInterval(poll, 4000)
  await loadInvoices()
}

async function poll() {
  ticks++
  try {
    await loadInvoices()
    const inv = invoices.value.find(i => i.id === payingInvoice.value)
    if (inv?.status === 'paid') return finish('paid')
    if (inv?.status === 'failed') return finish('failed')
    if (ticks >= 30) return finish('failed') // two minutes without an answer
  } catch { /* transient: keep polling */ }
}
async function finish(result: 'paid' | 'failed') {
  clearInterval(timer)
  paying.value = result
  if (result === 'paid') {
    await loadSubscription()
    await loadAccess() // lifts the paywall
    toast.success('Payment received. Thank you!')
  }
}
onBeforeUnmount(() => clearInterval(timer))

const invoiceStatus: Record<Invoice['status'], { status: BadgeStatus; label: string }> = {
  paid: { status: 'paid', label: 'Paid' }, pending: { status: 'pending', label: 'Pending' }, failed: { status: 'arrears', label: 'Failed' },
}
async function openInvoice(id: string) {
  try { await api.openPdf(`${base.value}/invoices/${id}/pdf`) } catch (e) { toast.fail(e) }
}
</script>

<template>
  <div class="grid max-w-4xl gap-8">
    <h2 v-if="productInfo" class="-mb-4">{{ productInfo.label }} billing</h2>
    <UiFormAlert v-if="loadError">{{ loadError }} <button type="button" class="font-semibold text-accent-text" @click="load">Retry</button></UiFormAlert>
    <UiSkeleton v-if="loading" block />

    <template v-else>
      <!-- Standing: the paywall, the trial, or nothing when paid up. -->
      <div v-if="access?.state === 'expired'" class="rounded-md border-2 border-arrears bg-arrears-tint p-5" role="alert" data-testid="paywall">
        <p class="flex items-center gap-2 text-[length:var(--text-h3)] font-semibold text-gray-900"><Icon name="lucide:lock" class="size-5" />Your free trial has ended</p>
        <p class="mt-1 text-gray-700">Choose a plan below to keep using {{ productInfo?.label ?? 'this product' }}. Your data is safe and waiting.<span v-if="product === 'rentals'"> Your tenants can still pay their rent in the meantime.</span></p>
      </div>
      <UiFormAlert v-else-if="access?.state === 'trial'" tone="info">
        You are on the free trial: <strong>{{ access.days_left }} {{ access.days_left === 1 ? 'day' : 'days' }} left</strong>. Subscribe now or any time before it ends.
      </UiFormAlert>
      <UiFormAlert v-else-if="lapsed" tone="info">
        {{ sub ? 'Your subscription is not active.' : 'You have not subscribed yet.' }} Pay below to keep managing your properties.
      </UiFormAlert>

      <UiCard>
        <div class="flex flex-wrap items-start justify-between gap-4">
          <div>
            <p class="text-[length:var(--text-caption)] text-gray-500">Current plan</p>
            <h2>{{ sub?.plan.name ?? 'No plan' }}</h2>
            <p v-if="sub" class="money mt-1 text-gray-700">Ksh {{ cur.format(sub.plan.price) }} / {{ sub.plan.billing_interval }}<span v-if="sub.plan.unit_cap"> &middot; up to {{ sub.plan.unit_cap }} units</span></p>
          </div>
          <UiBadge v-if="sub" :status="subStatus.status" :label="subStatus.label" />
        </div>
        <p v-if="sub" class="mt-4 text-gray-500">Paid until <span class="font-semibold text-gray-900">{{ formatDate(sub.subscription.current_period_end) }}</span></p>

        <div class="mt-6 flex max-w-md flex-col gap-5">
          <div>
            <p class="th-text mb-2">Choose a plan</p>
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <button v-for="p in plans" :key="p.id" type="button" :disabled="tooSmall(p)"
                class="rounded-sm border p-4 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:border-border-default"
                :class="chosenPlan === p.id ? 'border-accent-primary bg-accent-primary-tint' : 'border-border-default bg-white hover:border-accent-primary'"
                @click="!tooSmall(p) && (chosenPlan = p.id)">
                <p class="flex items-center gap-2 font-semibold">{{ p.name }}<UiBadge v-if="p.is_test" status="pending" label="TEST" /></p>
                <p v-if="p.pricing_type === 'per_unit'" class="money mt-1 text-gray-700">
                  Ksh {{ cur.format(p.per_unit_price ?? '0') }} per unit &middot; min {{ cur.format(p.min_price ?? '0') }}
                </p>
                <p v-else class="money mt-1 text-gray-700">Ksh {{ cur.format(p.price) }} / {{ p.billing_interval }}</p>
                <p class="mt-1 text-[length:var(--text-caption)] text-gray-500">{{ p.unit_cap ? `Up to ${p.unit_cap} units` : 'No unit limit' }}</p>
                <p v-if="tooSmall(p)" class="mt-1 text-[length:var(--text-caption)] text-arrears-text">Your {{ unitCount }} units exceed this plan's limit of {{ p.unit_cap }}.</p>
              </button>
            </div>
            <p v-if="renew.fields.value.plan" class="mt-2 text-[length:var(--text-caption)] text-arrears-text">{{ renew.fields.value.plan }}</p>
          </div>
          <UiField label="M-Pesa phone" for="stk-phone" hint="The number that gets the STK prompt. Defaults to your firm phone." :error="renew.fields.value.phone">
            <UiInput id="stk-phone" v-model="stkPhone" type="tel" placeholder="+254712345678" :invalid="!!renew.fields.value.phone" />
          </UiField>
          <UiFormAlert v-if="renew.message.value">{{ renew.message.value }}</UiFormAlert>
          <UiFormAlert v-if="paying === 'waiting'" tone="info">Waiting for your M-Pesa PIN on the firm phone. This page updates on its own.</UiFormAlert>
          <UiFormAlert v-else-if="paying === 'paid'" tone="success">Payment confirmed.</UiFormAlert>
          <UiFormAlert v-else-if="paying === 'failed'">The payment did not go through. You can try again.</UiFormAlert>
          <div>
            <UiButton :loading="renew.loading.value || paying === 'waiting'" :disabled="paying === 'waiting' || !chosenPlan || !stkPhone.trim()" @click="startRenewal">
              {{ chosenPlanDetails ? payButtonLabel(chosenPlanDetails) : 'Choose a plan' }}
            </UiButton>
          </div>
        </div>
      </UiCard>

      <UiCard :padded="false">
        <h2 class="px-6 pt-6 pb-4">Invoices</h2>
        <UiEmptyState v-if="!invoices.length" title="No invoices yet" text="Invoices appear after your first payment attempt." />
        <div v-else class="overflow-x-auto">
          <table class="stack-table w-full text-left">
            <thead>
              <tr class="h-10 border-y border-border-default bg-gray-50">
                <th class="th-text px-5 font-medium">Date</th><th class="th-text px-5 font-medium">Period</th>
                <th class="th-text px-5 text-right font-medium">Amount</th><th class="th-text px-5 font-medium">Status</th><th class="w-12" />
              </tr>
            </thead>
            <tbody>
              <tr v-for="i in invoices" :key="i.id" class="h-12 border-b border-border-subtle hover:bg-blue-50">
                <td class="px-5" data-label="Date">{{ formatDate(i.created_at) }}</td>
                <td class="px-5" data-label="Period">{{ periodLabel(i.period.slice(0, 7)) }}</td>
                <td class="money px-5 text-right font-semibold" data-label="Amount">{{ cur.format(i.amount) }}</td>
                <td class="px-5" data-label="Status"><UiBadge :status="invoiceStatus[i.status].status" :label="invoiceStatus[i.status].label" /></td>
                <td class="px-3"><UiButton variant="icon" label="Open invoice PDF" @click="openInvoice(i.id)"><Icon name="lucide:file-text" class="size-5" /></UiButton></td>
              </tr>
            </tbody>
          </table>
          <UiPagination :meta="meta" @change="loadInvoices" />
        </div>
      </UiCard>
    </template>
  </div>
</template>
