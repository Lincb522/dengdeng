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
const loadingPayment = ref(true)
let paymentPollTimer: ReturnType<typeof window.setInterval> | null = null
let paymentPollBusy = false

const defaultPresetAmounts = [1, 10, 20, 50, 100, 200, 500, 1000]
const paymentAvailable = computed(() => payment.value?.enabled && (payment.value.methods?.length ?? 0) > 0)
const minorDivisor = computed(() => payment.value?.currency === 'JPY' ? 1 : 100)
const selectedAmountMinor = computed(() => toMinor(amount.value))
const selectedCreditMicro = computed(() => creditForMinor(selectedAmountMinor.value))
const amountIsValid = computed(() => {
  const minor = selectedAmountMinor.value
  if (!minor || !payment.value) return false
  return minor >= payment.value.min_amount_minor && minor <= payment.value.max_amount_minor
})
const presetAmounts = computed(() => {
  if (!payment.value) return defaultPresetAmounts
  const divisor = minorDivisor.value
  const min = payment.value.min_amount_minor / divisor
  const max = payment.value.max_amount_minor / divisor
  const candidates = defaultPresetAmounts.filter((value) => value >= min && value <= max)
  if (!candidates.some((value) => Math.abs(value - min) < 0.0001)) candidates.unshift(min)
  return [...new Set(candidates)].slice(0, 8)
})

function chargeLabel(minor: number, currency: string) {
  const digits = currency === 'JPY' ? 0 : 2
  return `${currency === 'CNY' ? '¥' : `${currency} `}${(minor / (digits ? 100 : 1)).toFixed(digits)}`
}

function methodLabel(value: string) {
  return ({ alipay: '支付宝', wxpay: '微信支付', card: '银行卡', link: 'Link' } as Record<string, string>)[value] || value
}

function methodMark(value: string) {
  return ({ alipay: '支', wxpay: '微', card: '卡', link: 'L' } as Record<string, string>)[value] || value.slice(0, 1).toUpperCase()
}

function statusLabel(value: string) {
  return ({ PENDING: '待支付', PAID: '已支付', COMPLETED: '已到账', EXPIRED: '已过期', CANCELLED: '已取消', FAILED: '失败', REFUND_REQUESTED: '退款待审核', REFUNDING: '退款处理中', REFUNDED: '已退款' } as Record<string, string>)[value] || value
}

function toMinor(value: string | number) {
  const number = Number(value)
  if (!Number.isFinite(number) || number <= 0) return 0
  return Math.round(number * minorDivisor.value)
}

function creditForMinor(minor: number) {
  if (!minor || !payment.value?.credit_micro_per_unit) return 0
  return Math.floor(minor * payment.value.credit_micro_per_unit / minorDivisor.value)
}

function selectAmount(value: number) {
  amount.value = Number.isInteger(value) ? String(value) : value.toFixed(2)
}

function isSelectedAmount(value: number) {
  return selectedAmountMinor.value === toMinor(value)
}

async function loadPayment() {
  loadingPayment.value = true
  const info = await withToast(() => api.get<PaymentCheckoutInfo>('/api/user/payment/config'))
  if (info) {
    payment.value = info
    if (!method.value || !info.methods.includes(method.value)) method.value = info.methods[0] || ''
    const current = toMinor(amount.value)
    if (current < info.min_amount_minor || current > info.max_amount_minor) {
      amount.value = String(presetAmounts.value[0] ?? info.min_amount_minor / minorDivisor.value)
    }
  }
  const list = await withToast(() => api.get<PaymentOrder[]>('/api/user/payment/orders?limit=12'))
  if (list) {
    orders.value = list
    if (!activeOrder.value) {
      activeOrder.value = list.find(order => order.status === 'PENDING' && Boolean(order.checkout?.qr_code || order.checkout?.client_secret)) || null
    }
  }
  loadingPayment.value = false
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
  if (!amountIsValid.value || !method.value) return
  paymentBusy.value = true
  const order = await withToast(() => api.post<PaymentOrder>('/api/user/payment/orders', { amount_minor: selectedAmountMinor.value, payment_method: method.value }))
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
    // Temporary provider failures are retried by the next poll.
  } finally {
    paymentPollBusy = false
  }
}

async function copy(value?: string) {
  if (!value) return
  await withToast(() => copyText(value), '已复制')
}

function formatExpiry(value: string | null | undefined) {
  return value ? new Date(value).toLocaleDateString() : '未开通'
}

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
  <div class="wallet-store">
    <header class="wallet-store-head">
      <div class="wallet-store-title">
        <span class="wallet-store-title-mark" aria-hidden="true">¥</span>
        <div><h1>充值中心</h1><p>购买余额或兑换充值码</p></div>
      </div>
      <div class="wallet-store-actions">
        <a href="#wallet-redeem" class="wallet-button wallet-button--secondary">兑换码</a>
        <button type="button" class="wallet-button wallet-button--secondary" :disabled="loadingPayment" @click="loadPayment">刷新</button>
      </div>
    </header>

    <div class="wallet-overview" aria-label="账户概况">
      <div><span>当前余额</span><strong>{{ formatMoney(auth.user?.balance_micro ?? 0) }}</strong></div>
      <div><span>按日有效期</span><strong>{{ formatExpiry(auth.user?.access_expires_at) }}</strong></div>
      <div><span>剩余次数</span><strong>{{ (auth.user?.remaining_requests ?? 0).toLocaleString() }}</strong></div>
      <div><span>计费倍率</span><strong>x{{ auth.user?.rate_multiplier ?? 1 }}</strong></div>
    </div>

    <div v-if="paymentAvailable" class="wallet-commerce">
      <main class="wallet-products">
        <div class="wallet-section-head">
          <div><h2>余额充值</h2><p>{{ payment?.product_name }}</p></div>
          <span>{{ presetAmounts.length }} 个金额</span>
        </div>

        <div v-if="loadingPayment" class="wallet-package-grid" aria-label="正在加载充值金额">
          <span v-for="item in 8" :key="item" class="wallet-package-skeleton"></span>
        </div>
        <div v-else class="wallet-package-grid">
          <button
            v-for="(value, index) in presetAmounts"
            :key="value"
            type="button"
            class="wallet-package"
            :class="[`wallet-package--tone-${index % 4}`, { 'is-selected': isSelectedAmount(value) }]"
            :aria-pressed="isSelectedAmount(value)"
            @click="selectAmount(value)"
          >
            <span class="wallet-package-check" aria-hidden="true">✓</span>
            <small>到账余额</small>
            <strong>{{ formatMoney(creditForMinor(toMinor(value))) }}</strong>
            <span class="wallet-package-payment"><small>支付金额</small><b>{{ chargeLabel(toMinor(value), payment?.currency ?? 'CNY') }}</b></span>
          </button>
        </div>

        <label class="wallet-custom-amount">
          <span><strong>自定义金额</strong><small>{{ chargeLabel(payment?.min_amount_minor ?? 0, payment?.currency ?? 'CNY') }} - {{ chargeLabel(payment?.max_amount_minor ?? 0, payment?.currency ?? 'CNY') }}</small></span>
          <span class="wallet-custom-input"><b>{{ payment?.currency === 'CNY' ? '¥' : payment?.currency }}</b><input v-model="amount" type="number" min="0" step="0.01" inputmode="decimal" aria-label="自定义充值金额" /></span>
        </label>
      </main>

      <aside class="wallet-checkout" aria-label="确认订单">
        <template v-if="activeOrder && (activeOrder.checkout?.qr_code || activeOrder.checkout?.client_secret)">
          <div class="wallet-checkout-heading">
            <span class="wallet-checkout-icon" aria-hidden="true">订</span>
            <div><strong>完成支付</strong><small>{{ activeOrder.out_trade_no }}</small></div>
            <button type="button" @click="activeOrder = null">收起</button>
          </div>
          <div v-if="activeOrder.checkout?.qr_code" class="wallet-qr-payment">
            <PaymentQrCode :value="activeOrder.checkout.qr_code" :label="`${methodLabel(activeOrder.payment_method)}支付二维码`" />
            <strong>{{ chargeLabel(activeOrder.amount_minor, activeOrder.currency) }}</strong>
            <span>请使用{{ methodLabel(activeOrder.payment_method) }}扫码</span>
            <button type="button" class="wallet-button wallet-button--secondary" @click="copy(activeOrder.checkout?.qr_code)">复制支付码</button>
          </div>
          <StripeCheckout v-else-if="activeOrder.provider_key === 'stripe' && activeOrder.checkout?.client_secret" :checkout="activeOrder.checkout" @complete="verify(activeOrder)" @error="toast.show($event, 'error')" />
          <AirwallexCheckout v-else-if="activeOrder.provider_key === 'airwallex' && activeOrder.checkout?.client_secret" :checkout="activeOrder.checkout" :currency="activeOrder.currency" @error="toast.show($event, 'error')" />
          <div v-else class="wallet-checkout-message">支付页面正在准备，请稍后刷新状态。</div>
          <button v-if="activeOrder.provider_key !== 'stripe' && activeOrder.provider_key !== 'airwallex'" type="button" class="wallet-button wallet-button--primary wallet-button--wide" @click="verify(activeOrder)">核验到账</button>
        </template>

        <template v-else>
          <div class="wallet-checkout-heading">
            <span class="wallet-checkout-icon" aria-hidden="true">订</span>
            <div><strong>确认订单</strong><small>{{ formatMoney(selectedCreditMicro) }} / {{ chargeLabel(selectedAmountMinor, payment?.currency ?? 'CNY') }}</small></div>
          </div>

          <div class="wallet-methods">
            <h3>支付方式</h3>
            <button
              v-for="item in payment?.methods"
              :key="item"
              type="button"
              :class="{ 'is-selected': method === item }"
              :aria-pressed="method === item"
              @click="method = item"
            >
              <span class="wallet-method-mark" :class="`is-${item}`">{{ methodMark(item) }}</span>
              <strong>{{ methodLabel(item) }}</strong>
              <i aria-hidden="true"></i>
            </button>
          </div>

          <dl class="wallet-order-summary">
            <div><dt>到账余额</dt><dd>{{ formatMoney(selectedCreditMicro) }}</dd></div>
            <div><dt>支付金额</dt><dd>{{ chargeLabel(selectedAmountMinor, payment?.currency ?? 'CNY') }}</dd></div>
          </dl>
          <div class="wallet-pay-total"><span>合计</span><strong>{{ chargeLabel(selectedAmountMinor, payment?.currency ?? 'CNY') }}</strong></div>
          <button type="button" class="wallet-button wallet-button--primary wallet-button--wide" :disabled="paymentBusy || !amountIsValid || !method" @click="createOrder">
            {{ paymentBusy ? '正在创建订单' : '确认支付' }}
          </button>
          <p>订单 {{ payment?.order_expiry_minutes }} 分钟内有效</p>
        </template>
      </aside>
    </div>

    <div v-else-if="payment && !payment.enabled" class="wallet-unavailable">在线充值暂未开放</div>

    <section id="wallet-redeem" class="wallet-redeem">
      <div><h2>兑换充值码</h2><p>输入可用的兑换码</p></div>
      <div><input v-model="code" placeholder="dd-gift-xxxxxxxx" @keyup.enter="redeem" /><button type="button" class="wallet-button wallet-button--primary" :disabled="redeeming || !code" @click="redeem">{{ redeeming ? '兑换中' : '兑换' }}</button></div>
    </section>

    <section class="wallet-orders">
      <header><div><h2>充值记录</h2><p>最近 12 笔订单</p></div><button type="button" class="wallet-button wallet-button--secondary" @click="loadPayment">刷新</button></header>
      <div v-if="orders.length" class="wallet-order-list">
        <article v-for="order in orders" :key="order.id">
          <div class="wallet-order-amount"><strong>{{ chargeLabel(order.amount_minor, order.currency) }}</strong><span>到账 {{ formatMoney(order.credit_micro) }}</span></div>
          <div class="wallet-order-meta"><strong>{{ methodLabel(order.payment_method) }}</strong><span>{{ new Date(order.created_at).toLocaleString() }}</span></div>
          <code>{{ order.out_trade_no }}</code>
          <div class="wallet-order-status"><span :class="`is-${order.status.toLowerCase()}`">{{ statusLabel(order.status) }}</span><button v-if="order.status === 'PENDING'" type="button" @click="verify(order)">核验</button></div>
        </article>
      </div>
      <div v-else class="wallet-order-empty">还没有充值记录</div>
    </section>
  </div>
</template>

<style scoped>
.wallet-store { width: min(100%, 92rem); color: var(--ink); }
.wallet-store-head { display: flex; min-height: 4.7rem; align-items: center; justify-content: space-between; gap: 1rem; margin: -1.35rem -1.35rem 1rem; padding: .85rem 1.35rem; border-bottom: 1px solid var(--line); background: color-mix(in srgb, var(--surface) 88%, transparent); }
.wallet-store-title { display: flex; min-width: 0; align-items: center; gap: .8rem; }
.wallet-store-title-mark { display: grid; width: 2.5rem; height: 2.5rem; flex: 0 0 auto; place-items: center; border-radius: .62rem; background: var(--accent); color: #231c13; font-size: 1rem; font-weight: 900; }
.wallet-store-title h1 { font-size: 1rem; font-weight: 850; }
.wallet-store-title p { margin-top: .12rem; color: var(--ink-soft); font-size: .7rem; }
.wallet-store-actions { display: flex; gap: .45rem; }
.wallet-button { display: inline-flex; min-height: 2.35rem; align-items: center; justify-content: center; padding: 0 .85rem; border-radius: .5rem; font-size: .72rem; font-weight: 780; transition: border-color .16s ease, background .16s ease, color .16s ease, transform .16s ease; white-space: nowrap; }
.wallet-button:active { transform: scale(.98); }
.wallet-button:disabled { cursor: not-allowed; opacity: .52; }
.wallet-button--secondary { border: 1px solid var(--line); background: var(--surface); color: var(--ink); }
.wallet-button--secondary:hover:not(:disabled) { border-color: var(--accent); }
.wallet-button--primary { background: var(--accent); color: #241b10; }
.wallet-button--primary:hover:not(:disabled) { background: rgb(var(--dd-amber-bright)); }
.wallet-button--wide { width: 100%; min-height: 3rem; }
.wallet-overview { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); overflow: hidden; margin-bottom: 1rem; border: 1px solid var(--line); border-radius: .75rem; background: var(--surface); }
.wallet-overview div { display: grid; min-width: 0; gap: .2rem; padding: .75rem 1rem; border-right: 1px solid var(--line); }
.wallet-overview div:last-child { border-right: 0; }
.wallet-overview span { color: var(--ink-soft); font-size: .62rem; }
.wallet-overview strong { overflow: hidden; font-size: .82rem; text-overflow: ellipsis; white-space: nowrap; }
.wallet-commerce { display: grid; grid-template-columns: minmax(0, 1fr) 24rem; align-items: start; gap: 1rem; }
.wallet-products { min-width: 0; }
.wallet-section-head { display: flex; min-height: 3.1rem; align-items: start; justify-content: space-between; gap: 1rem; }
.wallet-section-head h2,
.wallet-redeem h2,
.wallet-orders h2 { font-size: .88rem; font-weight: 850; }
.wallet-section-head p,
.wallet-redeem p,
.wallet-orders header p { margin-top: .18rem; color: var(--ink-soft); font-size: .66rem; }
.wallet-section-head > span { padding: .35rem .55rem; border-radius: .42rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .62rem; }
.wallet-package-grid { display: grid; grid-template-columns: repeat(4, minmax(10rem, 1fr)); gap: .68rem; }
.wallet-package { position: relative; display: grid; min-width: 0; min-height: 11rem; align-content: space-between; overflow: hidden; padding: 1rem; border: 1px solid var(--line); border-radius: .66rem; background: var(--surface); color: var(--ink); text-align: left; transition: border-color .18s ease, transform .18s ease, background .18s ease; }
.wallet-package--tone-0 { background: color-mix(in srgb, var(--surface) 90%, rgb(var(--dd-signal-cyan))); }
.wallet-package--tone-1 { background: color-mix(in srgb, var(--surface) 91%, rgb(var(--dd-signal-green))); }
.wallet-package--tone-2 { background: color-mix(in srgb, var(--surface) 91%, #8f70d8); }
.wallet-package--tone-3 { background: color-mix(in srgb, var(--surface) 91%, var(--accent)); }
.wallet-package:hover { border-color: color-mix(in srgb, var(--accent) 66%, var(--line)); transform: translateY(-2px); }
.wallet-package.is-selected { border-color: var(--accent); background: color-mix(in srgb, var(--surface) 84%, var(--accent)); box-shadow: inset 0 0 0 1px var(--accent); }
.wallet-package > small { color: var(--ink-soft); font-size: .64rem; }
.wallet-package > strong { overflow: hidden; margin: .35rem 0 1.1rem; font-size: clamp(1.45rem, 2vw, 2rem); letter-spacing: -.035em; text-overflow: ellipsis; white-space: nowrap; }
.wallet-package-check { position: absolute; top: .7rem; right: .7rem; display: none; width: 1.35rem; height: 1.35rem; place-items: center; border-radius: 50%; background: var(--accent); color: #241b10; font-size: .7rem; font-weight: 900; }
.wallet-package.is-selected .wallet-package-check { display: grid; }
.wallet-package-payment { display: flex; align-items: end; justify-content: space-between; gap: .5rem; padding-top: .8rem; border-top: 1px solid color-mix(in srgb, var(--line) 72%, transparent); }
.wallet-package-payment small { color: var(--ink-soft); font-size: .61rem; }
.wallet-package-payment b { font-size: .72rem; }
.wallet-package-skeleton { min-height: 11rem; border-radius: .66rem; background: var(--surface-muted); animation: wallet-pulse 1.2s ease-in-out infinite alternate; }
.wallet-custom-amount { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-top: .7rem; padding: .75rem .85rem; border: 1px solid var(--line); border-radius: .66rem; background: var(--surface); }
.wallet-custom-amount > span:first-child { display: grid; gap: .18rem; }
.wallet-custom-amount strong { font-size: .72rem; }
.wallet-custom-amount small { color: var(--ink-soft); font-size: .61rem; }
.wallet-custom-input { display: flex; width: min(13rem, 48%); min-height: 2.45rem; align-items: center; overflow: hidden; border: 1px solid var(--line); border-radius: .48rem; background: var(--surface-muted); }
.wallet-custom-input b { padding-left: .75rem; color: var(--ink-soft); font-size: .7rem; }
.wallet-custom-input input { width: 100%; min-width: 0; padding: 0 .7rem; background: transparent; color: var(--ink); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .78rem; outline: none; }
.wallet-checkout { position: sticky; top: 1rem; display: grid; gap: 1rem; min-width: 0; padding: 1rem; border: 1px solid var(--line); border-radius: .75rem; background: var(--surface-muted); }
.wallet-checkout-heading { display: flex; min-width: 0; align-items: center; gap: .7rem; padding: .75rem; border: 1px solid var(--line); border-radius: .62rem; background: var(--surface); }
.wallet-checkout-icon { display: grid; width: 2.25rem; height: 2.25rem; flex: 0 0 auto; place-items: center; border-radius: .48rem; background: var(--ink); color: var(--surface); font-size: .66rem; font-weight: 850; }
.wallet-checkout-heading > div { display: grid; min-width: 0; flex: 1; gap: .12rem; }
.wallet-checkout-heading strong { font-size: .78rem; }
.wallet-checkout-heading small { overflow: hidden; color: var(--ink-soft); font-size: .62rem; text-overflow: ellipsis; white-space: nowrap; }
.wallet-checkout-heading > button { color: var(--ink-soft); font-size: .62rem; }
.wallet-methods { display: grid; gap: .48rem; }
.wallet-methods h3 { margin-bottom: .1rem; font-size: .75rem; font-weight: 820; }
.wallet-methods > button { display: flex; min-height: 3.7rem; align-items: center; gap: .7rem; padding: .6rem .75rem; border: 1px solid var(--line); border-radius: .55rem; background: var(--surface); color: var(--ink); text-align: left; }
.wallet-methods > button.is-selected { border-color: rgb(var(--dd-signal-green)); background: color-mix(in srgb, var(--surface) 91%, rgb(var(--dd-signal-green))); }
.wallet-method-mark { display: grid; width: 1.8rem; height: 1.8rem; flex: 0 0 auto; place-items: center; border-radius: .4rem; background: var(--surface-muted); color: var(--ink); font-size: .65rem; font-weight: 900; }
.wallet-method-mark.is-alipay { background: #e8f3ff; color: #1677ff; }
.wallet-method-mark.is-wxpay { background: #ecfae8; color: #16a20a; }
.wallet-methods strong { flex: 1; font-size: .72rem; }
.wallet-methods i { width: .68rem; height: .68rem; border: 2px solid var(--line); border-radius: 50%; }
.wallet-methods > button.is-selected i { border: 3px solid color-mix(in srgb, rgb(var(--dd-signal-green)) 24%, transparent); background: rgb(var(--dd-signal-green)); }
.wallet-order-summary { display: grid; padding: .25rem .85rem; border: 1px solid var(--line); border-radius: .58rem; background: var(--surface); }
.wallet-order-summary div { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .8rem 0; border-bottom: 1px solid var(--line); }
.wallet-order-summary div:last-child { border-bottom: 0; }
.wallet-order-summary dt { color: var(--ink-soft); font-size: .66rem; }
.wallet-order-summary dd { font-size: .7rem; font-weight: 800; }
.wallet-pay-total { display: flex; align-items: end; justify-content: space-between; gap: 1rem; padding: .1rem .85rem 0; }
.wallet-pay-total span { color: var(--ink-soft); font-size: .68rem; }
.wallet-pay-total strong { color: var(--accent); font-size: 1.55rem; letter-spacing: -.03em; }
.wallet-checkout > p { color: var(--ink-soft); font-size: .59rem; text-align: center; }
.wallet-qr-payment { display: grid; justify-items: center; gap: .5rem; padding: .8rem; border-radius: .6rem; background: var(--surface); text-align: center; }
.wallet-qr-payment strong { font-size: 1.25rem; }
.wallet-qr-payment > span { color: var(--ink-soft); font-size: .67rem; }
.wallet-checkout-message { padding: 1rem; border: 1px solid var(--line); border-radius: .55rem; background: var(--surface); color: var(--ink-soft); font-size: .68rem; line-height: 1.6; }
.wallet-redeem,
.wallet-orders { margin-top: 1rem; border: 1px solid var(--line); border-radius: .75rem; background: var(--surface); }
.wallet-redeem { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem; scroll-margin-top: 1rem; }
.wallet-redeem > div:last-child { display: flex; width: min(30rem, 58%); gap: .55rem; }
.wallet-redeem input { width: 100%; min-width: 0; min-height: 2.5rem; padding: 0 .78rem; border: 1px solid var(--line); border-radius: .5rem; background: var(--surface-muted); color: var(--ink); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .72rem; outline: none; }
.wallet-orders { overflow: hidden; }
.wallet-orders > header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .85rem 1rem; border-bottom: 1px solid var(--line); }
.wallet-order-list { display: grid; }
.wallet-order-list article { display: grid; grid-template-columns: minmax(9rem, .8fr) minmax(9rem, .8fr) minmax(12rem, 1fr) auto; align-items: center; gap: 1rem; padding: .85rem 1rem; border-bottom: 1px solid var(--line); }
.wallet-order-list article:last-child { border-bottom: 0; }
.wallet-order-amount,
.wallet-order-meta { display: grid; min-width: 0; gap: .18rem; }
.wallet-order-amount strong,
.wallet-order-meta strong { font-size: .72rem; }
.wallet-order-amount span,
.wallet-order-meta span { overflow: hidden; color: var(--ink-soft); font-size: .61rem; text-overflow: ellipsis; white-space: nowrap; }
.wallet-order-list code { overflow: hidden; color: var(--ink-soft); font-size: .61rem; text-overflow: ellipsis; white-space: nowrap; }
.wallet-order-status { display: flex; align-items: center; justify-content: flex-end; gap: .5rem; }
.wallet-order-status span { padding: .28rem .45rem; border-radius: .35rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .61rem; white-space: nowrap; }
.wallet-order-status span.is-completed { background: color-mix(in srgb, var(--surface) 88%, rgb(var(--dd-signal-green))); color: rgb(var(--dd-signal-green)); }
.wallet-order-status span.is-failed,
.wallet-order-status span.is-expired { color: rgb(var(--dd-signal-red)); }
.wallet-order-status button { color: var(--accent); font-size: .62rem; font-weight: 800; }
.wallet-order-empty,
.wallet-unavailable { padding: 2.5rem 1rem; border: 1px solid var(--line); border-radius: .75rem; background: var(--surface); color: var(--ink-soft); font-size: .72rem; text-align: center; }
.wallet-store :is(button, a, input):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }

@keyframes wallet-pulse { from { opacity: .55; } to { opacity: 1; } }

@media (max-width: 1180px) {
  .wallet-commerce { grid-template-columns: minmax(0, 1fr) 20rem; }
  .wallet-package-grid { grid-template-columns: repeat(3, minmax(10rem, 1fr)); }
}

@media (max-width: 900px) {
  .wallet-commerce { grid-template-columns: 1fr; }
  .wallet-checkout { position: static; }
  .wallet-overview { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .wallet-overview div:nth-child(2) { border-right: 0; }
  .wallet-overview div:nth-child(-n + 2) { border-bottom: 1px solid var(--line); }
  .wallet-order-list article { grid-template-columns: 1fr 1fr auto; }
  .wallet-order-list code { grid-column: 1 / 3; grid-row: 2; }
  .wallet-order-status { grid-column: 3; grid-row: 1 / 3; }
}

@media (max-width: 640px) {
  .wallet-store-head { min-height: auto; align-items: flex-start; flex-direction: column; margin: -.85rem -.85rem .8rem; padding: .85rem; }
  .wallet-store-actions { width: 100%; }
  .wallet-store-actions .wallet-button { flex: 1; }
  .wallet-overview { grid-template-columns: 1fr 1fr; }
  .wallet-overview div { padding: .7rem; }
  .wallet-package-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .5rem; }
  .wallet-package { min-height: 9.6rem; padding: .75rem; }
  .wallet-package > strong { font-size: 1.25rem; }
  .wallet-custom-amount { align-items: stretch; flex-direction: column; }
  .wallet-custom-input { width: 100%; }
  .wallet-redeem { align-items: stretch; flex-direction: column; }
  .wallet-redeem > div:last-child { width: 100%; }
  .wallet-order-list article { grid-template-columns: minmax(0, 1fr) auto; gap: .65rem; }
  .wallet-order-meta { grid-column: 1; }
  .wallet-order-list code { grid-column: 1 / -1; grid-row: auto; }
  .wallet-order-status { grid-column: 2; grid-row: 1 / 3; }
}

@media (prefers-reduced-motion: reduce) {
  .wallet-package,
  .wallet-button { transition: none; }
  .wallet-package-skeleton { animation: none; }
}
</style>
