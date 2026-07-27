<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, copyText, withToast } from '../api/client'
import type { ReferralDashboard, ReferralPayout } from '../api/types'
import { formatMoney } from '../api/types'
import { useToast } from '../stores/toast'

type PromotionTemplate = {
  id: string
  name: string
  description: string
  content: string
}

const toast = useToast()
const data = ref<ReferralDashboard | null>(null)
const loading = ref(true)
const bindCode = ref('')
const payoutOpenID = ref('')
const payoutBusy = ref(false)
const activePromotionID = ref('short')

const referralCodes = computed(() => Array.isArray(data.value?.codes) ? data.value.codes : [])
const commissions = computed(() => Array.isArray(data.value?.commissions) ? data.value.commissions : [])
const primaryCode = computed(() => referralCodes.value[0] || null)
const payouts = computed(() => Array.isArray(data.value?.payouts) ? data.value.payouts : [])
const referralLink = computed(() => primaryCode.value
  ? `${window.location.origin}/login?ref=${encodeURIComponent(primaryCode.value.code)}`
  : '')
const promotionTemplates = computed<PromotionTemplate[]>(() => {
  if (!primaryCode.value || !referralLink.value) return []

  const code = primaryCode.value.code
  const link = referralLink.value
  return [
    {
      id: 'short',
      name: '简短分享',
      description: '私聊、群聊',
      content: `DengDeng AI，多模型 API 接入与用量管理。\n注册：${link}\n推广码：${code}`,
    },
    {
      id: 'developer',
      name: '开发者接入',
      description: '开发者社区',
      content: `DengDeng AI 提供 OpenAI、Anthropic、Gemini 兼容接口，支持模型查询、独立密钥和用量明细。\n注册：${link}\n推广码：${code}`,
    },
    {
      id: 'cli',
      name: 'CLI 工具',
      description: '工具用户',
      content: `Claude Code、Codex CLI、Gemini CLI 和 Chatbox 可以通过 DengDeng AI 快速配置接入。注册后创建密钥即可使用。\n注册：${link}\n推广码：${code}`,
    },
    {
      id: 'social',
      name: '日常分享',
      description: '朋友圈、公告',
      content: `最近在用 DengDeng AI 管理多模型 API，密钥、模型和用量可以集中查看。有需要可以从这里注册：\n${link}\n推广码：${code}`,
    },
  ]
})
const activePromotion = computed(() => promotionTemplates.value.find((item) => item.id === activePromotionID.value)
  || promotionTemplates.value[0]
  || null)

async function load() {
  loading.value = true
  try {
    const payload = await api.get<ReferralDashboard>('/api/user/referrals')
    data.value = {
      ...payload,
      binding: payload?.binding || null,
      codes: Array.isArray(payload?.codes) ? payload.codes : [],
      commissions: Array.isArray(payload?.commissions) ? payload.commissions : [],
      total_commission_micro: Number(payload?.total_commission_micro) || 0,
		cash: payload.cash || { pending_micro: 0, available_micro: 0, locked_micro: 0, paid_micro: 0, currency: 'CNY', total_minor: 0, pending_minor: 0, available_minor: 0, locked_minor: 0, paid_minor: 0, min_payout_minor: 100, enabled: false },
		payout_account: payload.payout_account || null,
		payouts: Array.isArray(payload.payouts) ? payload.payouts : [],
    }
  } finally {
    loading.value = false
  }
}

function cashMoney(minor: number, currency = 'CNY') {
	const symbol = currency === 'CNY' ? '¥' : `${currency} `
	return `${symbol}${(minor / 100).toFixed(2)}`
}

function payoutStatus(value: string) {
	return ({ REVIEW_PENDING: '待审核', QUEUED: '待提交', SUBMITTING: '提交中', AWAITING_CONFIRMATION: '待确认收款', PROCESSING: '转账中', STATUS_UNCERTAIN: '待核验', SUCCESS: '已到账', FAILED: '失败', CANCELLED: '已取消' } as Record<string, string>)[value] || value
}

async function savePayoutAccount() {
	if (!payoutOpenID.value.trim()) return
	payoutBusy.value = true
	const saved = await withToast(() => api.post('/api/user/referrals/payout-account', { openid: payoutOpenID.value.trim() }), '微信收款账户已提交审核')
	payoutBusy.value = false
	if (saved) { payoutOpenID.value = ''; await load() }
}

async function requestPayout() {
	payoutBusy.value = true
	const saved = await withToast(() => api.post('/api/user/referrals/payouts', {}), '提现申请已提交')
	payoutBusy.value = false
	if (saved) await load()
}

function confirmInWeChat(item: ReferralPayout) {
	const bridge = (window as any).WeixinJSBridge
	if (!bridge || !item.package_info || !item.app_id || !item.merchant_id) {
		toast.show('请在微信内打开本页确认收款', 'error')
		return
	}
	bridge.invoke('requestMerchantTransfer', { mchId: item.merchant_id, appId: item.app_id, package: item.package_info }, () => load())
}

async function createCode() {
  const result = await withToast(() => api.post('/api/user/referrals/code', {}), '推广码已生成')
  if (result) await load()
}

async function bind() {
  if (!bindCode.value.trim()) return
  const result = await withToast(
    () => api.post('/api/user/referrals/bind', { code: bindCode.value.trim() }),
    '推广码已绑定',
  )
  if (result) {
    bindCode.value = ''
    await load()
  }
}

async function copy(value: string, message: string) {
  try {
    await copyText(value)
    toast.show(message, 'success')
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '复制失败', 'error')
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>推广中心</h1>
      </div>
    </div>

    <div v-if="loading" class="card p-8 text-sm text-slate-500">正在读取…</div>
    <template v-else-if="data">
      <section class="mb-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <article class="card p-5">
          <div class="label">累计佣金 Commission</div>
          <div class="mt-2 num text-2xl font-semibold text-signal-green">{{ cashMoney(data.cash.total_minor, data.cash.currency) }}</div>
        </article>
		<article class="card p-5">
			<div class="label">可提现 Available</div>
			<div class="mt-2 num text-2xl font-semibold text-signal-green">{{ cashMoney(data.cash.available_minor, data.cash.currency) }}</div>
		</article>
		<article class="card p-5">
			<div class="label">结算中 Pending</div>
			<div class="mt-2 num text-2xl font-semibold text-slate-200">{{ cashMoney(data.cash.pending_minor, data.cash.currency) }}</div>
		</article>
		<article class="card p-5">
			<div class="label">提现中 Locked</div>
			<div class="mt-2 num text-2xl font-semibold text-amber">{{ cashMoney(data.cash.locked_minor, data.cash.currency) }}</div>
		</article>
        <article class="card p-5">
          <div class="label">推广用户</div>
          <div class="mt-2 num text-2xl font-semibold text-slate-200">{{ primaryCode?.referred_users || 0 }}</div>
        </article>
        <article class="card p-5">
          <div class="label">当前比例 Rate</div>
          <div class="mt-2 num text-2xl font-semibold text-amber">{{ primaryCode ? `${(primaryCode.commission_bps / 100).toFixed(2)}%` : '—' }}</div>
        </article>
      </section>

	  <section class="card mb-6 p-6">
		<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
			<div><h2 class="text-sm font-semibold text-slate-200">现金结算</h2><p class="mt-1 text-xs text-slate-500">微信 OpenID 必须属于本站商户绑定的 AppID，审核通过后才可提现。</p></div>
			<button v-if="data.payout_account?.status === 'verified'" class="btn-primary" :disabled="payoutBusy || !data.cash.enabled || data.cash.available_minor < data.cash.min_payout_minor" @click="requestPayout">{{ payoutBusy ? '处理中…' : `提现 ${cashMoney(data.cash.available_minor, data.cash.currency)}` }}</button>
		</div>
		<div v-if="data.payout_account" class="flex flex-wrap items-center gap-3 text-sm">
			<span class="tag-gray font-mono">{{ data.payout_account.openid_hint }}</span>
			<span :class="data.payout_account.status === 'verified' ? 'tag-green' : data.payout_account.status === 'disabled' ? 'tag-red' : 'tag-gray'">{{ data.payout_account.status === 'verified' ? '已验证' : data.payout_account.status === 'disabled' ? '已停用' : '待审核' }}</span>
			<span v-if="data.payout_account.note" class="text-xs text-slate-500">{{ data.payout_account.note }}</span>
		</div>
		<div v-else class="flex flex-col gap-2 sm:flex-row">
			<input v-model.trim="payoutOpenID" class="input flex-1 font-mono" maxlength="64" placeholder="微信收款 OpenID" />
			<button class="btn-primary" :disabled="payoutBusy || payoutOpenID.length < 8" @click="savePayoutAccount">提交审核</button>
		</div>
		<p v-if="!data.cash.enabled" class="mt-3 text-xs text-amber">管理员尚未启用现金提现。</p>
	  </section>

	  <section v-if="payouts.length" class="card mb-6 overflow-x-auto">
		<div class="border-b border-slate-800 px-5 py-4"><h2 class="text-sm font-semibold text-slate-200">提现记录 Payouts</h2></div>
		<table v-responsive-table class="table-base">
			<thead><tr><th>时间</th><th>单号</th><th>收款账户</th><th>状态</th><th class="text-right">金额</th><th class="text-right">操作</th></tr></thead>
			<tbody><tr v-for="item in payouts" :key="item.id"><td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(item.requested_at).toLocaleString() }}</td><td><code class="font-mono text-xs">{{ item.out_bill_no }}</code></td><td class="font-mono text-xs">{{ item.openid_hint }}</td><td><span :class="item.status === 'SUCCESS' ? 'tag-green' : item.status === 'FAILED' || item.status === 'CANCELLED' ? 'tag-red' : 'tag-gray'">{{ payoutStatus(item.status) }}</span><p v-if="item.failure_message" class="mt-1 max-w-xs text-xs text-signal-red">{{ item.failure_message }}</p></td><td class="num text-right">{{ cashMoney(item.amount_minor, item.currency) }}</td><td class="text-right"><button v-if="item.status === 'AWAITING_CONFIRMATION'" class="btn-primary !px-3 !py-1.5 text-xs" @click="confirmInWeChat(item)">确认收款</button></td></tr></tbody>
		</table>
	  </section>

      <section class="card mb-6 p-6">
        <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 class="text-sm font-semibold text-slate-200">我的推广码 My Code</h2>
            <p class="mt-1 text-xs text-slate-500">新用户注册时填写推广码，或打开推广链接自动带入。</p>
          </div>
          <button v-if="!primaryCode" class="btn-primary" @click="createCode">生成推广码</button>
        </div>
        <div v-if="primaryCode" class="space-y-3">
          <div class="flex flex-col gap-2 sm:flex-row">
            <code class="input flex-1 select-all font-mono">{{ primaryCode.code }}</code>
            <button class="btn-ghost" @click="copy(primaryCode.code, '推广码已复制')">复制推广码</button>
          </div>
          <div class="flex flex-col gap-2 sm:flex-row">
            <code class="input flex-1 truncate font-mono text-xs">{{ referralLink }}</code>
            <button class="btn-ghost" @click="copy(referralLink, '推广链接已复制')">复制推广链接</button>
          </div>
          <p v-if="primaryCode.status !== 'active'" class="text-xs text-signal-red">该推广码已暂停，不再产生新绑定或佣金。</p>
        </div>
        <p v-else class="text-sm text-slate-500">还没有推广码。</p>
      </section>

      <section v-if="primaryCode" class="referral-promotion card mb-6">
        <header class="referral-promotion__head">
          <div>
            <h2>推广文案 Promotion Copy</h2>
          </div>
          <button
            class="btn-primary referral-promotion__copy"
            type="button"
            :disabled="!activePromotion || primaryCode.status !== 'active'"
            @click="activePromotion && copy(activePromotion.content, '推广文案已复制')"
          >
            复制当前文案
          </button>
        </header>

        <div class="referral-promotion__workspace">
          <nav class="referral-promotion__tabs" aria-label="推广文案类型">
            <button
              v-for="item in promotionTemplates"
              :key="item.id"
              type="button"
              :class="{ 'is-active': activePromotion?.id === item.id }"
              :aria-pressed="activePromotion?.id === item.id"
              @click="activePromotionID = item.id"
            >
              <strong>{{ item.name }}</strong>
              <span>{{ item.description }}</span>
            </button>
          </nav>

          <div class="referral-promotion__preview" aria-live="polite">
            <div class="referral-promotion__preview-head">
              <span>{{ activePromotion?.name }}</span>
              <button
                type="button"
                :disabled="!activePromotion || primaryCode.status !== 'active'"
                @click="activePromotion && copy(activePromotion.content, '推广文案已复制')"
              >
                复制
              </button>
            </div>
            <p>{{ activePromotion?.content }}</p>
          </div>
        </div>
        <p v-if="primaryCode.status !== 'active'" class="referral-promotion__notice">推广码已暂停，恢复后才可复制推广文案。</p>
      </section>

      <section class="card mb-6 p-6">
        <h2 class="text-sm font-semibold text-slate-200">我使用的推广码 Bound Code</h2>
        <div v-if="data.binding" class="mt-4 flex flex-wrap items-center gap-3 text-sm">
          <span class="tag-green">{{ data.binding.code }}</span>
          <span class="text-slate-500">推广者 {{ data.binding.referrer_email }}</span>
          <span class="text-slate-500">{{ new Date(data.binding.bound_at).toLocaleString() }}</span>
        </div>
        <div v-else class="mt-4 flex flex-col gap-2 sm:flex-row">
          <input v-model.trim="bindCode" class="input flex-1 font-mono uppercase" maxlength="32" placeholder="输入推广码" @keyup.enter="bind" />
          <button class="btn-primary" :disabled="!bindCode" @click="bind">绑定</button>
        </div>
        <p v-if="!data.binding" class="mt-2 text-xs text-slate-500">绑定后不可自行更换。</p>
      </section>

      <section class="card overflow-x-auto">
        <div class="border-b border-slate-800 px-5 py-4">
          <h2 class="text-sm font-semibold text-slate-200">佣金明细 Commission Ledger</h2>
        </div>
        <table v-responsive-table class="table-base">
          <thead><tr><th>时间</th><th>用户</th><th>推广码</th><th>状态</th><th class="text-right">用户消费</th><th class="text-right">比例</th><th class="text-right">佣金</th></tr></thead>
          <tbody>
            <tr v-for="item in commissions" :key="item.id">
              <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(item.created_at).toLocaleString() }}</td>
              <td class="text-xs text-slate-300">{{ item.referred_email || `#${item.referred_user_id}` }}</td>
              <td><span class="tag-gray font-mono">{{ item.code }}</span></td>
			  <td><span class="tag-gray">{{ item.status === 'pending' ? '结算中' : item.status === 'available' ? '可提现' : item.status === 'legacy_balance' ? '历史余额结算' : '已冲正' }}</span></td>
              <td class="num text-right text-xs">{{ formatMoney(item.base_cost_micro) }}</td>
              <td class="num text-right text-xs">{{ (item.commission_bps / 100).toFixed(2) }}%</td>
              <td class="num text-right text-xs text-signal-green">+{{ formatMoney(item.amount_micro) }}</td>
            </tr>
            <tr v-if="!commissions.length"><td colspan="7" class="py-10 text-center text-sm text-slate-500">暂无佣金记录</td></tr>
          </tbody>
        </table>
      </section>
    </template>
  </div>
</template>
