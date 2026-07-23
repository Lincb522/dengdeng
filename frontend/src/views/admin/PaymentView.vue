<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { PaymentOrder, PaymentProvider } from '../../api/types'
import { formatMoney } from '../../api/types'

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

const defaults: PaymentConfig = { enabled: false, currency: 'CNY', credit_micro_per_unit: 0, min_amount_minor: 100, max_amount_minor: 1000000, daily_limit_minor: 0, order_expiry_minutes: 30, max_pending_orders: 3, load_balance_strategy: 'round_robin', product_name: 'DengDeng AI 账户充值' }
const config = ref<PaymentConfig>({ ...defaults })
const providers = ref<PaymentProvider[]>([])
const orders = ref<PaymentOrder[]>([])
const configBusy = ref(false)
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

function money(minor: number, currency = 'CNY') { return `${currency === 'CNY' ? '¥' : `${currency} `}${(minor / (currency === 'JPY' ? 1 : 100)).toFixed(currency === 'JPY' ? 0 : 2)}` }
function status(order: PaymentOrder) { return ({ PENDING: '待支付', COMPLETED: '已到账', FAILED: '失败', EXPIRED: '已过期', CANCELLED: '已取消', REFUND_REQUESTED: '退款申请', REFUNDING: '退款处理中', REFUNDED: '已退款' } as Record<string, string>)[order.status] || order.status }

async function load() {
  const [savedConfig, savedProviders, savedOrders] = await Promise.all([
    withToast(() => api.get<PaymentConfig>('/api/admin/payment/config')),
    withToast(() => api.get<PaymentProvider[]>('/api/admin/payment/providers')),
    withToast(() => api.get<PaymentOrder[]>('/api/admin/payment/orders?limit=100')),
  ])
  if (savedConfig) config.value = { ...defaults, ...savedConfig }
  if (savedProviders) providers.value = savedProviders
  if (savedOrders) orders.value = savedOrders
}

async function saveConfig() {
  configBusy.value = true
  const saved = await withToast(() => api.put<PaymentConfig>('/api/admin/payment/config', config.value), '支付设置已保存')
  configBusy.value = false
  if (saved) config.value = { ...defaults, ...saved }
}

function startProvider(provider?: PaymentProvider) {
  editingID.value = provider?.id ?? null
  providerForm.value = provider ? { name: provider.name, provider_key: provider.provider_key, currency: provider.currency, supported_methods: provider.supported_methods, payment_mode: provider.payment_mode, status: provider.status, priority: provider.priority, min_amount_minor: provider.min_amount_minor, max_amount_minor: provider.max_amount_minor, daily_limit_minor: provider.daily_limit_minor, config: '{\n  \n}' } : { name: '', provider_key: 'easypay', currency: 'CNY', supported_methods: 'alipay,wxpay', payment_mode: 'qrcode', status: 'active', priority: 10, min_amount_minor: 0, max_amount_minor: 0, daily_limit_minor: 0, config: '{\n  \n}' }
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
  if (saved) orders.value = orders.value.map(item => item.id === saved.id ? saved : item)
}

async function reconcileRefund(order: PaymentOrder) {
  const saved = await withToast(() => api.post<PaymentOrder>(`/api/admin/payment/orders/${order.id}/refund/query`), '退款状态已核验')
  if (saved) orders.value = orders.value.map(item => item.id === saved.id ? saved : item)
}

onMounted(load)
</script>

<template>
  <div class="max-w-6xl">
    <div class="console-page-head"><h1>支付中心</h1><p class="mt-1 text-sm text-slate-500">配置收款渠道、充值汇率和退款订单。渠道密钥仅加密保存，列表不会回显。</p></div>

    <section class="card mb-6 p-6">
      <div class="mb-5 flex items-center justify-between"><div><h3 class="text-sm font-semibold text-slate-100">充值规则</h3><p class="mt-1 text-xs text-slate-500">开启前请在服务配置中设置 site.public_url 为 HTTPS 公网地址。</p></div><label class="flex items-center gap-2 text-sm text-slate-300"><input v-model="config.enabled" type="checkbox" /> 开启在线充值</label></div>
      <div class="grid gap-4 md:grid-cols-4"><label><span class="form-label">结算币种</span><select v-model="config.currency" class="input w-full"><option>CNY</option><option>USD</option><option>HKD</option></select></label><label><span class="form-label">每 1 单位到账（微美元）</span><input v-model.number="config.credit_micro_per_unit" class="input num w-full" type="number" min="0" /></label><label><span class="form-label">最小金额（分）</span><input v-model.number="config.min_amount_minor" class="input num w-full" type="number" min="1" /></label><label><span class="form-label">最大金额（分）</span><input v-model.number="config.max_amount_minor" class="input num w-full" type="number" min="1" /></label><label><span class="form-label">每日上限（分，0 不限）</span><input v-model.number="config.daily_limit_minor" class="input num w-full" type="number" min="0" /></label><label><span class="form-label">订单有效分钟</span><input v-model.number="config.order_expiry_minutes" class="input num w-full" type="number" min="5" /></label><label><span class="form-label">每人待支付订单数</span><input v-model.number="config.max_pending_orders" class="input num w-full" type="number" min="1" /></label><label><span class="form-label">分流策略</span><select v-model="config.load_balance_strategy" class="input w-full"><option value="round_robin">轮询</option><option value="least_amount">最少收款额</option></select></label></div>
      <label class="mt-4 block max-w-xl"><span class="form-label">支付商品名称</span><input v-model="config.product_name" class="input w-full" /></label>
      <div class="mt-5 flex items-center justify-between gap-4"><p class="text-xs text-amber">当前汇率：{{ formatMoney(config.credit_micro_per_unit) }} / {{ config.currency }} 1</p><button class="btn-primary" :disabled="configBusy" @click="saveConfig">保存规则</button></div>
    </section>

    <section class="card mb-6 overflow-hidden">
      <div class="flex items-center justify-between px-6 py-4"><div><h3 class="text-sm font-semibold text-slate-100">收款渠道</h3><p class="mt-1 text-xs text-slate-500">一个渠道可以配置多个商户实例，系统根据分流策略选择。</p></div><button class="btn-primary" @click="startProvider()">添加渠道</button></div>
      <div v-if="providers.length" class="divide-y divide-slate-800"><div v-for="provider in providers" :key="provider.id" class="flex flex-wrap items-center justify-between gap-4 px-6 py-4"><div><div class="text-sm font-medium text-slate-200">{{ provider.name }} <span class="ml-2 font-mono text-xs text-amber">{{ provider.provider_key }}</span></div><div class="mt-1 text-xs text-slate-500">{{ provider.currency }} · {{ provider.supported_methods || '默认方式' }} · {{ provider.payment_mode }} · 优先级 {{ provider.priority }}</div></div><div class="flex items-center gap-3"><span class="text-xs" :class="provider.status === 'active' ? 'text-signal-green' : 'text-slate-500'">{{ provider.status === 'active' ? '启用' : '停用' }}</span><button class="btn-ghost text-xs" @click="startProvider(provider)">编辑</button><button class="btn-ghost text-xs !text-rose-400" @click="removeProvider(provider)">删除</button></div></div></div>
      <div v-else class="px-6 py-10 text-center text-sm text-slate-500">尚未配置收款渠道</div>
    </section>

    <section class="card overflow-hidden">
      <div class="flex items-center justify-between px-6 py-4">
        <div><h3 class="text-sm font-semibold text-slate-100">支付订单</h3><p class="mt-1 text-xs text-slate-500">每笔订单保留充值用户；退款会先冻结充值得到的余额。</p></div>
        <button class="btn-ghost text-xs" @click="load">刷新</button>
      </div>
      <div v-if="orders.length" class="divide-y divide-slate-800">
        <div v-for="order in orders" :key="order.id" class="payment-order-row">
          <div class="payment-order-user">
            <span>{{ (order.user_email || 'U').slice(0, 1).toUpperCase() }}</span>
            <div><strong>{{ order.user_email || `用户 #${order.user_id}` }}</strong><small>用户 ID {{ order.user_id }}</small></div>
          </div>
          <div class="payment-order-amount">
            <strong>{{ money(order.amount_minor, order.currency) }}</strong>
            <small>到账 {{ formatMoney(order.credit_micro) }}</small>
          </div>
          <div class="payment-order-meta">
            <code>{{ order.out_trade_no }}</code>
            <small>{{ order.provider_key }} · {{ new Date(order.created_at).toLocaleString() }}</small>
          </div>
          <div class="payment-order-actions">
            <span>{{ status(order) }}</span>
            <button v-if="order.status === 'REFUND_REQUESTED' || order.status === 'COMPLETED'" class="btn-ghost text-xs !text-amber" @click="refund(order)">处理退款</button>
            <button v-else-if="order.status === 'REFUNDING'" class="btn-ghost text-xs !text-amber" @click="reconcileRefund(order)">核验退款</button>
          </div>
        </div>
      </div>
      <div v-else class="px-6 py-10 text-center text-sm text-slate-500">暂无支付订单</div>
    </section>

    <div v-if="formOpen" class="fixed inset-0 z-50 flex items-center justify-center bg-ink-950/55 p-4" @click.self="formOpen = false"><div class="card max-h-[90vh] w-full max-w-2xl overflow-y-auto p-6"><div class="mb-5 flex items-start justify-between"><div><h3 class="text-base font-semibold text-slate-100">{{ editingID ? '更新收款渠道' : '添加收款渠道' }}</h3><p class="mt-1 text-xs text-amber">更新渠道时需重新提交完整密钥配置，系统不会回显已保存的敏感字段。</p></div><button class="btn-ghost" @click="formOpen = false">关闭</button></div><div class="grid gap-4 sm:grid-cols-2"><label><span class="form-label">名称</span><input v-model="providerForm.name" class="input w-full" /></label><label><span class="form-label">渠道</span><select v-model="providerForm.provider_key" class="input w-full"><option value="easypay">EasyPay</option><option value="alipay">支付宝官方</option><option value="wxpay">微信支付 API v3</option><option value="stripe">Stripe</option><option value="airwallex">Airwallex</option></select></label><label><span class="form-label">币种</span><input v-model="providerForm.currency" class="input w-full" /></label><label><span class="form-label">支付方式（逗号分隔）</span><input v-model="providerForm.supported_methods" class="input w-full" placeholder="alipay,wxpay" /></label><label><span class="form-label">展示模式</span><input v-model="providerForm.payment_mode" class="input w-full" placeholder="qrcode" /></label><label><span class="form-label">优先级</span><input v-model.number="providerForm.priority" class="input num w-full" type="number" /></label></div><label class="mt-4 block"><span class="form-label">密钥配置 JSON</span><textarea v-model="providerForm.config" class="input min-h-48 w-full font-mono text-xs" spellcheck="false"></textarea><span class="mt-2 block text-xs text-slate-500">{{ providerGuide }}</span></label><div class="mt-5 flex justify-end gap-3"><button class="btn-ghost" @click="formOpen = false">取消</button><button class="btn-primary" :disabled="providerBusy" @click="saveProvider">保存渠道</button></div></div></div>
  </div>
</template>
