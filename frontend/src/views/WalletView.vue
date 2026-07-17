<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, copyText, withToast } from '../api/client'
import type { PaymentCheckoutInfo, PaymentOrder } from '../api/types'
import { formatMoney } from '../api/types'
import { useAuth } from '../stores/auth'
import { useToast } from '../stores/toast'
import StripeCheckout from '../components/payment/StripeCheckout.vue'
import AirwallexCheckout from '../components/payment/AirwallexCheckout.vue'
import PaymentQrCode from '../components/payment/PaymentQrCode.vue'

const auth = useAuth()
const toast = useToast()
const code = ref('')
const redeeming = ref(false)
const paymentBusy = ref(false)
const payment = ref<PaymentCheckoutInfo | null>(null)
const orders = ref<PaymentOrder[]>([])
const amount = ref('10')
const method = ref('')
const activeOrder = ref<PaymentOrder | null>(null)
let paymentPollTimer: ReturnType<typeof window.setInterval> | null = null
let paymentPollBusy = false

const paymentAvailable = computed(() => payment.value?.enabled && (payment.value.methods?.length ?? 0) > 0)
const currencySymbol = computed(() => payment.value?.currency === 'CNY' ? '¥' : `${payment.value?.currency ?? ''} `)

function chargeLabel(minor: number, currency: string) {
  const digits = currency === 'JPY' ? 0 : 2
  return `${currency === 'CNY' ? '¥' : `${currency} `}${(minor / (digits ? 100 : 1)).toFixed(digits)}`
}

function methodLabel(value: string) {
  return ({ alipay: '支付宝', wxpay: '微信支付', card: '银行卡', link: 'Link' } as Record<string, string>)[value] || value
}

function statusLabel(value: string) {
  return ({ PENDING: '待支付', PAID: '已支付', COMPLETED: '已到账', EXPIRED: '已过期', CANCELLED: '已取消', FAILED: '失败', REFUND_REQUESTED: '退款待审核', REFUNDING: '退款处理中', REFUNDED: '已退款' } as Record<string, string>)[value] || value
}

function toMinor(value: string) {
  const number = Number(value)
  if (!Number.isFinite(number) || number <= 0) return 0
  return Math.round(number * (payment.value?.currency === 'JPY' ? 1 : 100))
}

async function loadPayment() {
  const info = await withToast(() => api.get<PaymentCheckoutInfo>('/api/user/payment/config'))
  if (info) {
    payment.value = info
    if (!method.value || !info.methods.includes(method.value)) method.value = info.methods[0] || ''
  }
  const list = await withToast(() => api.get<PaymentOrder[]>('/api/user/payment/orders?limit=12'))
  if (list) {
    orders.value = list
    if (!activeOrder.value) {
      activeOrder.value = list.find(order => order.status === 'PENDING' && Boolean(order.checkout?.qr_code || order.checkout?.client_secret)) || null
    }
  }
}

async function redeem() {
  if (!code.value) return
  redeeming.value = true
  const result = await withToast(() => api.post('/api/user/redeem', { code: code.value.trim() }), '兑换成功')
  redeeming.value = false
  if (result) {
    code.value = ''
    await auth.fetchMe()
  }
}

async function createOrder() {
  const amountMinor = toMinor(amount.value)
  if (!amountMinor || !method.value) return
  paymentBusy.value = true
  const order = await withToast(() => api.post<PaymentOrder>('/api/user/payment/orders', { amount_minor: amountMinor, payment_method: method.value }))
  paymentBusy.value = false
  if (!order) return
  activeOrder.value = order
  orders.value = [order, ...orders.value.filter(item => item.id !== order.id)]
  if (order.checkout?.pay_url) window.location.assign(order.checkout.pay_url)
}

async function verify(order: PaymentOrder) {
  const updated = await withToast(() => api.post<PaymentOrder>(`/api/user/payment/orders/${order.id}/verify`), '订单状态已刷新')
  if (!updated) return
  await applyOrderUpdate(updated, false)
}

async function applyOrderUpdate(updated: PaymentOrder, announce: boolean) {
  orders.value = orders.value.map(item => item.id === updated.id ? updated : item)
  if (activeOrder.value?.id === updated.id) activeOrder.value = updated
  if (updated.status !== 'COMPLETED') return
  await auth.fetchMe()
  if (announce) toast.show('支付已到账，余额已更新', 'success')
  if (activeOrder.value?.id === updated.id) activeOrder.value = null
}

async function pollPendingOrder() {
  const order = activeOrder.value
  if (!order || order.status !== 'PENDING' || paymentPollBusy) return
  paymentPollBusy = true
  try {
    const updated = await api.post<PaymentOrder>(`/api/user/payment/orders/${order.id}/verify`)
    await applyOrderUpdate(updated, true)
  } catch {
    // A temporary provider query failure is retried on the next pass. Manual
    // verification remains available and still shows its error to the user.
  } finally {
    paymentPollBusy = false
  }
}

async function copy(value?: string) {
  if (!value) return
  await withToast(() => copyText(value), '已复制')
}

function formatExpiry(value: string | null | undefined) { return value ? new Date(value).toLocaleDateString() : '未开通' }

onMounted(async () => {
  await loadPayment()
  await pollPendingOrder()
  paymentPollTimer = window.setInterval(() => { void pollPendingOrder() }, 5000)
})

onBeforeUnmount(() => {
  if (paymentPollTimer) window.clearInterval(paymentPollTimer)
})
</script>

<template>
  <div class="max-w-4xl">
    <div class="console-page-head">
      <h1>钱包</h1>
      <p class="mt-1 text-sm text-slate-500">管理余额、充值记录和兑换码</p>
    </div>

    <div class="card mb-6 flex items-center justify-between p-6">
      <div>
        <div class="text-[11px] uppercase tracking-widest text-slate-500">当前余额</div>
        <div class="num mt-1 text-3xl font-bold text-signal-green">{{ formatMoney(auth.user?.balance_micro ?? 0) }}</div>
      </div>
      <div class="text-right text-xs text-slate-500">
        <div>计费倍率 <span class="num text-slate-300">x{{ auth.user?.rate_multiplier ?? 1 }}</span></div>
        <div class="mt-1">余额、有效期或次数任一可用时均可调用</div>
      </div>
    </div>

    <div class="mb-6 grid gap-4 sm:grid-cols-2">
      <div class="card p-5"><div class="text-xs text-slate-500">按日有效期</div><div class="mt-1 text-lg font-semibold text-slate-200">{{ formatExpiry(auth.user?.access_expires_at) }}</div></div>
      <div class="card p-5"><div class="text-xs text-slate-500">剩余调用次数</div><div class="num mt-1 text-lg font-semibold text-slate-200">{{ (auth.user?.remaining_requests ?? 0).toLocaleString() }} 次</div></div>
    </div>

    <div v-if="paymentAvailable" class="card mb-6 p-6">
      <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div><h3 class="text-sm font-semibold text-slate-100">在线充值</h3><p class="mt-1 text-xs text-slate-500">{{ payment?.product_name }} · 每 {{ currencySymbol }}1 充值 {{ formatMoney(payment?.credit_micro_per_unit ?? 0) }}</p></div>
        <span class="tag-amber">安全支付</span>
      </div>
      <div class="grid gap-3 sm:grid-cols-[1fr_180px_auto]">
        <label class="block"><span class="mb-1 block text-xs text-slate-500">充值金额（{{ payment?.currency }}）</span><input v-model="amount" class="input num w-full" type="number" min="0" step="0.01" /></label>
        <label class="block"><span class="mb-1 block text-xs text-slate-500">支付方式</span><select v-model="method" class="input w-full"><option v-for="item in payment?.methods" :key="item" :value="item">{{ methodLabel(item) }}</option></select></label>
        <button class="btn-primary self-end" :disabled="paymentBusy || !toMinor(amount) || !method" @click="createOrder">去支付</button>
      </div>
      <p class="mt-3 text-xs text-slate-500">单笔 {{ chargeLabel(payment?.min_amount_minor ?? 0, payment?.currency ?? 'CNY') }} 至 {{ chargeLabel(payment?.max_amount_minor ?? 0, payment?.currency ?? 'CNY') }}；订单 {{ payment?.order_expiry_minutes }} 分钟内有效。</p>
    </div>
    <div v-else-if="payment" class="card mb-6 p-5 text-sm text-slate-500">在线充值暂未开放，请使用兑换码或联系管理员。</div>

    <div v-if="activeOrder && (activeOrder.checkout?.qr_code || activeOrder.checkout?.client_secret)" class="card mb-6 border-amber/30 p-6">
      <div class="flex items-start justify-between gap-4"><div><h3 class="text-sm font-semibold text-slate-100">等待完成支付</h3><p class="mt-1 text-xs text-slate-500">订单 {{ activeOrder.out_trade_no }}，支付成功后可点击“核验到账”。</p></div><button class="btn-ghost text-xs" @click="activeOrder = null">收起</button></div>
      <div v-if="activeOrder.checkout?.qr_code" class="payment-qr-checkout mt-4 rounded-lg border border-slate-700 bg-slate-950/40 p-4">
        <PaymentQrCode :value="activeOrder.checkout.qr_code" :label="`${methodLabel(activeOrder.payment_method)}支付二维码`" />
        <div class="min-w-0 flex-1"><div class="mb-2 text-xs text-slate-500">请使用{{ methodLabel(activeOrder.payment_method) }}扫码完成支付。二维码只在本页本地生成，不会发送至第三方。</div><div class="break-all font-mono text-xs text-slate-300">{{ activeOrder.checkout.qr_code }}</div><button class="btn-ghost mt-3 text-xs" @click="copy(activeOrder.checkout?.qr_code)">复制支付码</button></div>
      </div>
      <StripeCheckout v-else-if="activeOrder.provider_key === 'stripe' && activeOrder.checkout?.client_secret" :checkout="activeOrder.checkout" @complete="verify(activeOrder)" @error="toast.show($event, 'error')" />
      <AirwallexCheckout v-else-if="activeOrder.provider_key === 'airwallex' && activeOrder.checkout?.client_secret" :checkout="activeOrder.checkout" :currency="activeOrder.currency" @error="toast.show($event, 'error')" />
      <div v-else class="mt-4 rounded-lg border border-slate-700 bg-slate-950/40 p-4 text-xs text-slate-400">此渠道正在准备支付页面，请稍后刷新订单状态。</div>
      <button v-if="activeOrder.provider_key !== 'stripe' && activeOrder.provider_key !== 'airwallex'" class="btn-primary mt-4" @click="verify(activeOrder)">核验到账</button>
    </div>

    <div class="card mb-6 p-6">
      <h3 class="mb-4 text-sm font-semibold text-slate-100">使用兑换码</h3>
      <div class="flex gap-3"><input v-model="code" class="input flex-1 font-mono" placeholder="dd-gift-xxxxxxxx" @keyup.enter="redeem" /><button class="btn-primary" :disabled="redeeming || !code" @click="redeem">兑换</button></div>
    </div>

    <div class="card overflow-hidden">
      <div class="flex items-center justify-between px-6 py-4"><h3 class="text-sm font-semibold text-slate-100">充值记录</h3><button class="btn-ghost text-xs" @click="loadPayment">刷新</button></div>
      <div v-if="orders.length" class="divide-y divide-slate-800"><div v-for="order in orders" :key="order.id" class="flex flex-wrap items-center justify-between gap-3 px-6 py-4"><div><div class="text-sm text-slate-200">{{ chargeLabel(order.amount_minor, order.currency) }} <span class="ml-2 text-xs text-slate-500">→ {{ formatMoney(order.credit_micro) }}</span></div><div class="mt-1 font-mono text-[11px] text-slate-500">{{ order.out_trade_no }} · {{ new Date(order.created_at).toLocaleString() }}</div></div><div class="flex items-center gap-2"><span class="text-xs text-slate-400">{{ statusLabel(order.status) }}</span><button v-if="order.status === 'PENDING'" class="btn-ghost text-xs" @click="verify(order)">核验</button></div></div></div>
      <div v-else class="px-6 py-8 text-center text-sm text-slate-500">还没有充值记录</div>
    </div>
  </div>
</template>
