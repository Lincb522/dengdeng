<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { PaymentLedgerPage, PaymentOrder, PaymentProvider } from '../../api/types'
import { formatMoney } from '../../api/types'
import Pagination from '../../components/Pagination.vue'

type PaymentConfig = {
  enabled: boolean
  currency: string
  credit_micro_per_unit: number
  min_amount_minor: number
  max_amount_minor: number
  daily_limit_minor: number
  order_expiry_minutes: number
  max_pending_orders: number
  load_balance_strategy: string
  product_name: string
}

type PaymentTab = 'overview' | 'ledger' | 'orders' | 'settings'

const defaults: PaymentConfig = { enabled: false, currency: 'CNY', credit_micro_per_unit: 0, min_amount_minor: 100, max_amount_minor: 1000000, daily_limit_minor: 0, order_expiry_minutes: 30, max_pending_orders: 3, load_balance_strategy: 'round_robin', product_name: 'DengDeng AI 账户充值' }
const emptyLedger: PaymentLedgerPage = {
  items: [],
  total: 0,
  page: 1,
  size: 20,
  period: '30d',
  summary: { currency: 'CNY', income_minor: 0, expense_minor: 0, net_minor: 0, income_credit_micro: 0, expense_credit_micro: 0, income_count: 0, expense_count: 0 },
  trend: [],
  currencies: [],
  providers: [],
}

const activeTab = ref<PaymentTab>('overview')
const config = ref<PaymentConfig>({ ...defaults })
const providers = ref<PaymentProvider[]>([])
const orders = ref<PaymentOrder[]>([])
const ledger = ref<PaymentLedgerPage>({ ...emptyLedger, summary: { ...emptyLedger.summary } })
const ledgerFilters = ref({ period: '30d', kind: '', currency: 'CNY', provider: '', user: '', page: 1, size: 20 })
const configBusy = ref(false)
const ledgerBusy = ref(false)
const formOpen = ref(false)
const providerBusy = ref(false)
const editingID = ref<number | null>(null)
const providerForm = ref({ name: '', provider_key: 'easypay', currency: 'CNY', supported_methods: 'alipay,wxpay', payment_mode: 'qrcode', status: 'active', priority: 10, min_amount_minor: 0, max_amount_minor: 0, daily_limit_minor: 0, config: '{\n  \n}' })

const providerGuide = computed(() => ({
  easypay: '必填：pid、pkey、apiBase；可选：paymentMode（qrcode / popup）。',
  alipay: '必填：appId、privateKey、alipayPublicKey；可选：gateway、paymentMode（qrcode / redirect）。',
  wxpay: '必填：appId、mchId、serialNo、privateKey、platformCert、apiV3Key；可选：paymentMode（native / h5）。',
  stripe: '必填：secretKey、webhookSecret；建议同时填写 publishableKey。',
  airwallex: '必填：clientId、apiKey、apiBase（/api/v1）；线上还需 webhookSecret。',
} as Record<string, string>)[providerForm.value.provider_key] || '')

const periodLabel = computed(() => ({ '7d': '近 7 天', '30d': '近 30 天', '90d': '近 90 天', all: '全部时间' } as Record<string, string>)[ledgerFilters.value.period])
const trendRows = computed(() => ledger.value.trend.slice(-14))
const trendMax = computed(() => Math.max(1, ...trendRows.value.flatMap(item => [item.income_minor, item.expense_minor])))

function money(minor: number, currency = 'CNY') {
  const unit = currency === 'CNY' ? '¥' : currency === 'HKD' ? 'HK$' : currency === 'USD' ? '$' : `${currency} `
  return `${unit}${(minor / (currency === 'JPY' ? 1 : 100)).toFixed(currency === 'JPY' ? 0 : 2)}`
}
function status(order: PaymentOrder) {
  return ({ PENDING: '待支付', COMPLETED: '已到账', FAILED: '失败', EXPIRED: '已过期', CANCELLED: '已取消', REFUND_REQUESTED: '退款申请', REFUNDING: '退款处理中', REFUNDED: '已退款' } as Record<string, string>)[order.status] || order.status
}
function localTime(value: string) { return new Date(value).toLocaleString() }
function shortDate(value: string) {
  const date = new Date(`${value}T00:00:00Z`)
  return date.toLocaleDateString(undefined, { month: '2-digit', day: '2-digit' })
}
function trendWidth(value: number) { return `${Math.max(value > 0 ? 5 : 0, (value / trendMax.value) * 100)}%` }

async function loadLedger() {
  ledgerBusy.value = true
  const params = new URLSearchParams({
    page: String(ledgerFilters.value.page),
    size: String(ledgerFilters.value.size),
    period: ledgerFilters.value.period,
    currency: ledgerFilters.value.currency,
  })
  if (ledgerFilters.value.kind) params.set('kind', ledgerFilters.value.kind)
  if (ledgerFilters.value.provider) params.set('provider', ledgerFilters.value.provider)
  if (ledgerFilters.value.user.trim()) params.set('user', ledgerFilters.value.user.trim())
  const saved = await withToast(() => api.get<PaymentLedgerPage>(`/api/admin/payment/ledger?${params.toString()}`))
  ledgerBusy.value = false
  if (saved) {
    saved.items ||= []
    saved.trend ||= []
    saved.currencies ||= []
    saved.providers ||= []
    ledger.value = saved
  }
}

async function load() {
  const [savedConfig, savedProviders, savedOrders] = await Promise.all([
    withToast(() => api.get<PaymentConfig>('/api/admin/payment/config')),
    withToast(() => api.get<PaymentProvider[]>('/api/admin/payment/providers')),
    withToast(() => api.get<PaymentOrder[]>('/api/admin/payment/orders?limit=100')),
  ])
  if (savedConfig) {
    config.value = { ...defaults, ...savedConfig }
    if (!ledger.value.total) ledgerFilters.value.currency = savedConfig.currency
  }
  if (savedProviders) providers.value = savedProviders
  if (savedOrders) orders.value = savedOrders
  await loadLedger()
}

async function applyLedgerFilters() {
  ledgerFilters.value.page = 1
  await loadLedger()
}

async function changeLedgerPage(page: number) {
  ledgerFilters.value.page = page
  await loadLedger()
}

async function saveConfig() {
  configBusy.value = true
  const saved = await withToast(() => api.put<PaymentConfig>('/api/admin/payment/config', config.value), '支付设置已保存')
  configBusy.value = false
  if (saved) config.value = { ...defaults, ...saved }
}

function startProvider(provider?: PaymentProvider) {
  editingID.value = provider?.id ?? null
  providerForm.value = provider
    ? { name: provider.name, provider_key: provider.provider_key, currency: provider.currency, supported_methods: provider.supported_methods, payment_mode: provider.payment_mode, status: provider.status, priority: provider.priority, min_amount_minor: provider.min_amount_minor, max_amount_minor: provider.max_amount_minor, daily_limit_minor: provider.daily_limit_minor, config: '{\n  \n}' }
    : { name: '', provider_key: 'easypay', currency: 'CNY', supported_methods: 'alipay,wxpay', payment_mode: 'qrcode', status: 'active', priority: 10, min_amount_minor: 0, max_amount_minor: 0, daily_limit_minor: 0, config: '{\n  \n}' }
  formOpen.value = true
}

async function saveProvider() {
  let rawConfig: Record<string, string>
  try {
    const parsed = JSON.parse(providerForm.value.config)
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error()
    rawConfig = Object.fromEntries(Object.entries(parsed).map(([key, value]) => [key, String(value)]))
  } catch {
    await withToast(async () => { throw new Error('渠道配置必须是 JSON 对象') })
    return
  }
  providerBusy.value = true
  const payload = { ...providerForm.value, config: rawConfig }
  const path = editingID.value ? `/api/admin/payment/providers/${editingID.value}` : '/api/admin/payment/providers'
  const request = editingID.value ? api.put<PaymentProvider>(path, payload) : api.post<PaymentProvider>(path, payload)
  const saved = await withToast(() => request, '渠道已保存')
  providerBusy.value = false
  if (saved) { formOpen.value = false; await load() }
}

async function removeProvider(provider: PaymentProvider) {
  if (!window.confirm(`删除“${provider.name}”？已有订单的渠道不能删除。`)) return
  const result = await withToast(() => api.delete(`/api/admin/payment/providers/${provider.id}`), '渠道已删除')
  if (result) await load()
}

async function refund(order: PaymentOrder) {
  if (!window.confirm(`处理 ${order.out_trade_no} 的退款？系统会先冻结对应余额。`)) return
  const saved = await withToast(() => api.post<PaymentOrder>(`/api/admin/payment/orders/${order.id}/refund`), '退款已提交')
  if (saved) {
    orders.value = orders.value.map(item => item.id === saved.id ? saved : item)
    await loadLedger()
  }
}

async function reconcileRefund(order: PaymentOrder) {
  const saved = await withToast(() => api.post<PaymentOrder>(`/api/admin/payment/orders/${order.id}/refund/query`), '退款状态已核验')
  if (saved) {
    orders.value = orders.value.map(item => item.id === saved.id ? saved : item)
    await loadLedger()
  }
}

onMounted(load)
</script>

<template>
  <div class="payment-center max-w-6xl">
    <div class="console-page-head">
      <div><h1>支付中心</h1><p class="mt-1 text-sm text-slate-500">收支、订单与渠道统一管理。</p></div>
      <button class="btn-ghost text-xs" :disabled="ledgerBusy" @click="load">刷新数据</button>
    </div>

    <nav class="payment-tabs" aria-label="支付中心导航">
      <button :class="{ 'is-active': activeTab === 'overview' }" @click="activeTab = 'overview'">收支概览</button>
      <button :class="{ 'is-active': activeTab === 'ledger' }" @click="activeTab = 'ledger'">记账本</button>
      <button :class="{ 'is-active': activeTab === 'orders' }" @click="activeTab = 'orders'">支付订单</button>
      <button :class="{ 'is-active': activeTab === 'settings' }" @click="activeTab = 'settings'">渠道设置</button>
    </nav>

    <template v-if="activeTab === 'overview'">
      <section class="payment-cashflow">
        <div class="payment-net">
          <span>{{ periodLabel }}净收入</span>
          <strong>{{ money(ledger.summary.net_minor, ledger.summary.currency) }}</strong>
          <small>{{ ledger.summary.income_count }} 笔收入 · {{ ledger.summary.expense_count }} 笔退款</small>
        </div>
        <dl class="payment-summary">
          <div><dt>收入</dt><dd class="is-income">+{{ money(ledger.summary.income_minor, ledger.summary.currency) }}</dd></div>
          <div><dt>退款支出</dt><dd class="is-expense">-{{ money(ledger.summary.expense_minor, ledger.summary.currency) }}</dd></div>
          <div><dt>发放额度</dt><dd>{{ formatMoney(ledger.summary.income_credit_micro) }}</dd></div>
          <div><dt>收回额度</dt><dd>{{ formatMoney(ledger.summary.expense_credit_micro) }}</dd></div>
        </dl>
      </section>

      <section class="card payment-trend">
        <div class="payment-section-head">
          <div><h3>每日收支</h3><p>仅展示最近 14 个有流水的日期，金额为实际支付币种。</p></div>
          <div class="payment-period">
            <button v-for="item in [['7d', '7 天'], ['30d', '30 天'], ['90d', '90 天'], ['all', '全部']]" :key="item[0]" :class="{ 'is-active': ledgerFilters.period === item[0] }" @click="ledgerFilters.period = item[0]; applyLedgerFilters()">{{ item[1] }}</button>
          </div>
        </div>
        <div v-if="trendRows.length" class="payment-trend-list">
          <div v-for="item in trendRows" :key="item.date" class="payment-trend-row">
            <time>{{ shortDate(item.date) }}</time>
            <div class="payment-trend-bars">
              <div><i class="is-income" :style="{ width: trendWidth(item.income_minor) }"></i></div>
              <div><i class="is-expense" :style="{ width: trendWidth(item.expense_minor) }"></i></div>
            </div>
            <div class="payment-trend-values">
              <span>+{{ money(item.income_minor, ledger.summary.currency) }} <small>{{ item.income_count }} 笔</small></span>
              <span>-{{ money(item.expense_minor, ledger.summary.currency) }} <small>{{ item.expense_count }} 笔</small></span>
            </div>
          </div>
        </div>
        <div v-else class="payment-empty">所选时间内暂无已到账或退款流水</div>
      </section>

      <section class="card payment-recent">
        <div class="payment-section-head">
          <div><h3>最近流水</h3><p>充值到账与退款成功后自动记账。</p></div>
          <button class="btn-ghost text-xs" @click="activeTab = 'ledger'">查看全部</button>
        </div>
        <div v-if="ledger.items.length" class="payment-ledger-list">
          <div v-for="item in ledger.items.slice(0, 6)" :key="item.id" class="payment-ledger-row">
            <span class="payment-ledger-kind" :class="`is-${item.kind}`">{{ item.kind === 'income' ? '收入' : '退款' }}</span>
            <div class="payment-ledger-user"><strong>{{ item.user_email || `用户 #${item.user_id}` }}</strong><small>用户 ID {{ item.user_id }}</small></div>
            <div class="payment-ledger-order"><code>{{ item.order_no }}</code><small>{{ item.provider_key }} · {{ item.payment_method || '默认方式' }}</small></div>
            <time>{{ localTime(item.occurred_at) }}</time>
            <div class="payment-ledger-amount" :class="`is-${item.kind}`"><strong>{{ item.kind === 'income' ? '+' : '-' }}{{ money(item.amount_minor, item.currency) }}</strong><small>{{ item.kind === 'income' ? '发放' : '收回' }} {{ formatMoney(item.credit_micro) }}</small></div>
          </div>
        </div>
        <div v-else class="payment-empty">暂无账本流水</div>
      </section>
    </template>

    <section v-else-if="activeTab === 'ledger'" class="card payment-ledger">
      <div class="payment-section-head">
        <div><h3>记账本</h3><p>每次充值到账和退款成功都会生成一条不可重复的流水。</p></div>
        <span class="payment-ledger-balance">净收入 {{ money(ledger.summary.net_minor, ledger.summary.currency) }}</span>
      </div>
      <div class="payment-ledger-filters">
        <select v-model="ledgerFilters.period" class="input" @change="applyLedgerFilters"><option value="7d">近 7 天</option><option value="30d">近 30 天</option><option value="90d">近 90 天</option><option value="all">全部时间</option></select>
        <select v-model="ledgerFilters.currency" class="input" @change="applyLedgerFilters"><option v-for="currency in (ledger.currencies.length ? ledger.currencies : [config.currency])" :key="currency">{{ currency }}</option></select>
        <select v-model="ledgerFilters.kind" class="input" @change="applyLedgerFilters"><option value="">全部收支</option><option value="income">仅收入</option><option value="expense">仅退款</option></select>
        <select v-model="ledgerFilters.provider" class="input" @change="applyLedgerFilters"><option value="">全部渠道</option><option v-for="provider in ledger.providers" :key="provider" :value="provider">{{ provider }}</option></select>
        <div class="payment-ledger-search"><input v-model="ledgerFilters.user" class="input" placeholder="用户邮箱或 ID" @keyup.enter="applyLedgerFilters" /><button class="btn-ghost" @click="applyLedgerFilters">查询</button></div>
      </div>
      <div v-if="ledger.items.length" class="payment-ledger-list">
        <div v-for="item in ledger.items" :key="item.id" class="payment-ledger-row">
          <span class="payment-ledger-kind" :class="`is-${item.kind}`">{{ item.kind === 'income' ? '收入' : '退款' }}</span>
          <div class="payment-ledger-user"><strong>{{ item.user_email || `用户 #${item.user_id}` }}</strong><small>用户 ID {{ item.user_id }}</small></div>
          <div class="payment-ledger-order"><code>{{ item.order_no }}</code><small>{{ item.provider_key }} · {{ item.payment_method || '默认方式' }}</small></div>
          <time>{{ localTime(item.occurred_at) }}</time>
          <div class="payment-ledger-amount" :class="`is-${item.kind}`"><strong>{{ item.kind === 'income' ? '+' : '-' }}{{ money(item.amount_minor, item.currency) }}</strong><small>{{ item.kind === 'income' ? '发放' : '收回' }} {{ formatMoney(item.credit_micro) }}</small></div>
        </div>
      </div>
      <div v-else class="payment-empty">没有符合条件的流水</div>
      <Pagination :page="ledger.page" :size="ledger.size" :total="ledger.total" @change="changeLedgerPage" />
    </section>

    <section v-else-if="activeTab === 'orders'" class="card overflow-hidden">
      <div class="payment-section-head">
        <div><h3>支付订单</h3><p>订单状态、充值用户和退款处理。</p></div>
        <span class="payment-order-count">最近 {{ orders.length }} 笔</span>
      </div>
      <div v-if="orders.length" class="divide-y divide-slate-800">
        <div v-for="order in orders" :key="order.id" class="payment-order-row">
          <div class="payment-order-user"><span>{{ (order.user_email || 'U').slice(0, 1).toUpperCase() }}</span><div><strong>{{ order.user_email || `用户 #${order.user_id}` }}</strong><small>用户 ID {{ order.user_id }}</small></div></div>
          <div class="payment-order-amount"><strong>{{ money(order.amount_minor, order.currency) }}</strong><small>到账 {{ formatMoney(order.credit_micro) }}</small></div>
          <div class="payment-order-meta"><code>{{ order.out_trade_no }}</code><small>{{ order.provider_key }} · {{ localTime(order.created_at) }}</small></div>
          <div class="payment-order-actions"><span>{{ status(order) }}</span><button v-if="order.status === 'REFUND_REQUESTED' || order.status === 'COMPLETED'" class="btn-ghost text-xs !text-amber" @click="refund(order)">处理退款</button><button v-else-if="order.status === 'REFUNDING'" class="btn-ghost text-xs !text-amber" @click="reconcileRefund(order)">核验退款</button></div>
        </div>
      </div>
      <div v-else class="payment-empty">暂无支付订单</div>
    </section>

    <template v-else>
      <section class="card mb-6 p-6">
        <div class="payment-section-head"><div><h3>充值规则</h3><p>开启前需设置 HTTPS 公网地址。</p></div><label class="flex items-center gap-2 text-sm text-slate-300"><input v-model="config.enabled" type="checkbox" /> 开启在线充值</label></div>
        <div class="grid gap-4 md:grid-cols-4"><label><span class="form-label">结算币种</span><select v-model="config.currency" class="input w-full"><option>CNY</option><option>USD</option><option>HKD</option></select></label><label><span class="form-label">每 1 单位到账（微美元）</span><input v-model.number="config.credit_micro_per_unit" class="input num w-full" type="number" min="0" /></label><label><span class="form-label">最小金额（分）</span><input v-model.number="config.min_amount_minor" class="input num w-full" type="number" min="1" /></label><label><span class="form-label">最大金额（分）</span><input v-model.number="config.max_amount_minor" class="input num w-full" type="number" min="1" /></label><label><span class="form-label">每日上限（分，0 不限）</span><input v-model.number="config.daily_limit_minor" class="input num w-full" type="number" min="0" /></label><label><span class="form-label">订单有效分钟</span><input v-model.number="config.order_expiry_minutes" class="input num w-full" type="number" min="5" /></label><label><span class="form-label">每人待支付订单数</span><input v-model.number="config.max_pending_orders" class="input num w-full" type="number" min="1" /></label><label><span class="form-label">分流策略</span><select v-model="config.load_balance_strategy" class="input w-full"><option value="round_robin">轮询</option><option value="least_amount">最少收款额</option></select></label></div>
        <label class="mt-4 block max-w-xl"><span class="form-label">支付商品名称</span><input v-model="config.product_name" class="input w-full" /></label>
        <div class="mt-5 flex items-center justify-between gap-4"><p class="text-xs text-amber">当前汇率：{{ formatMoney(config.credit_micro_per_unit) }} / {{ config.currency }} 1</p><button class="btn-primary" :disabled="configBusy" @click="saveConfig">保存规则</button></div>
      </section>

      <section class="card overflow-hidden">
        <div class="payment-section-head"><div><h3>收款渠道</h3><p>可配置多个商户实例，系统按分流策略选择。</p></div><button class="btn-primary" @click="startProvider()">添加渠道</button></div>
        <div v-if="providers.length" class="divide-y divide-slate-800"><div v-for="provider in providers" :key="provider.id" class="flex flex-wrap items-center justify-between gap-4 px-6 py-4"><div><div class="text-sm font-medium text-slate-200">{{ provider.name }} <span class="ml-2 font-mono text-xs text-amber">{{ provider.provider_key }}</span></div><div class="mt-1 text-xs text-slate-500">{{ provider.currency }} · {{ provider.supported_methods || '默认方式' }} · {{ provider.payment_mode }} · 优先级 {{ provider.priority }}</div></div><div class="flex items-center gap-3"><span class="text-xs" :class="provider.status === 'active' ? 'text-signal-green' : 'text-slate-500'">{{ provider.status === 'active' ? '启用' : '停用' }}</span><button class="btn-ghost text-xs" @click="startProvider(provider)">编辑</button><button class="btn-ghost text-xs !text-rose-400" @click="removeProvider(provider)">删除</button></div></div></div>
        <div v-else class="payment-empty">尚未配置收款渠道</div>
      </section>
    </template>

    <div v-if="formOpen" class="fixed inset-0 z-50 flex items-center justify-center bg-ink-950/55 p-4" @click.self="formOpen = false"><div class="card max-h-[90vh] w-full max-w-2xl overflow-y-auto p-6"><div class="mb-5 flex items-start justify-between"><div><h3 class="text-base font-semibold text-slate-100">{{ editingID ? '更新收款渠道' : '添加收款渠道' }}</h3><p class="mt-1 text-xs text-amber">更新渠道时需重新提交完整密钥配置，系统不会回显已保存的敏感字段。</p></div><button class="btn-ghost" @click="formOpen = false">关闭</button></div><div class="grid gap-4 sm:grid-cols-2"><label><span class="form-label">名称</span><input v-model="providerForm.name" class="input w-full" /></label><label><span class="form-label">渠道</span><select v-model="providerForm.provider_key" class="input w-full"><option value="easypay">EasyPay</option><option value="alipay">支付宝官方</option><option value="wxpay">微信支付 API v3</option><option value="stripe">Stripe</option><option value="airwallex">Airwallex</option></select></label><label><span class="form-label">币种</span><input v-model="providerForm.currency" class="input w-full" /></label><label><span class="form-label">支付方式（逗号分隔）</span><input v-model="providerForm.supported_methods" class="input w-full" placeholder="alipay,wxpay" /></label><label><span class="form-label">展示模式</span><input v-model="providerForm.payment_mode" class="input w-full" placeholder="qrcode" /></label><label><span class="form-label">优先级</span><input v-model.number="providerForm.priority" class="input num w-full" type="number" /></label></div><label class="mt-4 block"><span class="form-label">密钥配置 JSON</span><textarea v-model="providerForm.config" class="input min-h-48 w-full font-mono text-xs" spellcheck="false"></textarea><span class="mt-2 block text-xs text-slate-500">{{ providerGuide }}</span></label><div class="mt-5 flex justify-end gap-3"><button class="btn-ghost" @click="formOpen = false">取消</button><button class="btn-primary" :disabled="providerBusy" @click="saveProvider">保存渠道</button></div></div></div>
  </div>
</template>
