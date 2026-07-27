<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { PaymentProvider, ReferralCodeStats, ReferralPayout, ReferralPayoutAccount, User } from '../../api/types'
import { formatMoney } from '../../api/types'

type ReferralRow = ReferralCodeStats & { commission_percent: number }

const items = ref<ReferralRow[]>([])
const users = ref<User[]>([])
const ownerUserID = ref<number | null>(null)
const customCode = ref('')
const commissionPercent = ref(5)
const busy = ref(false)
const payoutBusy = ref(false)
const payoutAccounts = ref<ReferralPayoutAccount[]>([])
const payouts = ref<ReferralPayout[]>([])
const paymentProviders = ref<PaymentProvider[]>([])
const accountUserID = ref<number | null>(null)
const accountOpenID = ref('')
const payoutConfig = ref({ enabled: false, currency: 'CNY', settlement_days: 7, min_payout_minor: 100, max_payout_minor: 200000, daily_payout_minor: 500000, require_review: true, wx_provider_id: 0, wx_transfer_scene_id: '', scene_report_info_type: '', scene_report_info_content: '', transfer_remark: '推广佣金' })

function cashMoney(minor: number, currency = 'CNY') { return `${currency === 'CNY' ? '¥' : `${currency} `}${(minor / 100).toFixed(2)}` }
function payoutStatus(value: string) { return ({ REVIEW_PENDING: '待审核', QUEUED: '待提交', SUBMITTING: '提交中', AWAITING_CONFIRMATION: '待用户确认', PROCESSING: '转账中', STATUS_UNCERTAIN: '待核验', SUCCESS: '已到账', FAILED: '失败', CANCELLED: '已取消' } as Record<string, string>)[value] || value }

async function load() {
  const [codes, userList, cashConfig, accounts, payoutList, providers] = await Promise.all([
    api.get<ReferralCodeStats[]>('/api/admin/referral-codes'),
    api.get<User[]>('/api/admin/users'),
	api.get<typeof payoutConfig.value>('/api/admin/referral-payout/config'),
	api.get<ReferralPayoutAccount[]>('/api/admin/referral-payout/accounts'),
	api.get<ReferralPayout[]>('/api/admin/referral-payouts'),
	api.get<PaymentProvider[]>('/api/admin/payment/providers'),
  ])
  items.value = (Array.isArray(codes) ? codes : []).map((item) => ({ ...item, commission_percent: item.commission_bps / 100 }))
  users.value = Array.isArray(userList) ? userList : []
  if (!ownerUserID.value && users.value.length) ownerUserID.value = users.value[0].id
	if (!accountUserID.value && users.value.length) accountUserID.value = users.value[0].id
	payoutConfig.value = { ...payoutConfig.value, ...cashConfig }
	payoutAccounts.value = Array.isArray(accounts) ? accounts : []
	payouts.value = Array.isArray(payoutList) ? payoutList : []
	paymentProviders.value = (Array.isArray(providers) ? providers : []).filter(item => item.provider_key === 'wxpay' && item.status === 'active')
}

async function savePayoutConfig() {
	payoutBusy.value = true
	const saved = await withToast(() => api.put('/api/admin/referral-payout/config', payoutConfig.value), '现金分账设置已保存')
	payoutBusy.value = false
	if (saved) await load()
}

async function savePayoutAccount(userID: number, status: string, note = '') {
	const saved = await withToast(() => api.put(`/api/admin/referral-payout/accounts/${userID}`, { status, note }), status === 'verified' ? '收款账户已验证' : '收款账户状态已更新')
	if (saved) await load()
}

async function createPayoutAccount() {
	if (!accountUserID.value || !accountOpenID.value.trim()) return
	const saved = await withToast(() => api.put(`/api/admin/referral-payout/accounts/${accountUserID.value}`, { openid: accountOpenID.value.trim(), status: 'verified', note: '管理员验证' }), '微信收款账户已绑定')
	if (saved) { accountOpenID.value = ''; await load() }
}

async function payoutAction(item: ReferralPayout, action: 'approve' | 'reject' | 'query') {
	if (action === 'approve' && !window.confirm(`向 ${item.user_email || `用户 #${item.user_id}`} 转账 ${cashMoney(item.amount_minor, item.currency)}？`)) return
	const payload = action === 'reject' ? { reason: '管理员拒绝' } : {}
	const saved = await withToast(() => api.post(`/api/admin/referral-payouts/${item.id}/${action}`, payload), action === 'approve' ? '转账已提交' : action === 'query' ? '转账状态已核验' : '提现已拒绝')
	if (saved) await load()
}

async function create() {
  if (!ownerUserID.value) return
  busy.value = true
  const result = await withToast(() => api.post('/api/admin/referral-codes', {
    owner_user_id: ownerUserID.value,
    code: customCode.value.trim(),
    commission_bps: Math.round(Number(commissionPercent.value) * 100),
  }), '推广码已创建')
  busy.value = false
  if (result) {
    customCode.value = ''
    commissionPercent.value = 5
    await load()
  }
}

async function save(item: ReferralRow) {
  const result = await withToast(() => api.put(`/api/admin/referral-codes/${item.id}`, {
    commission_bps: Math.round(Number(item.commission_percent) * 100),
    status: item.status,
  }), '推广设置已保存')
  if (result) await load()
}

async function toggle(item: ReferralRow) {
  item.status = item.status === 'active' ? 'disabled' : 'active'
  await save(item)
}

async function remove(item: ReferralRow) {
  if (!confirm(`删除推广码 ${item.code}？已产生绑定的推广码只能暂停。`)) return
  const result = await withToast(() => api.delete(`/api/admin/referral-codes/${item.id}`), '推广码已删除')
  if (result) await load()
}

onMounted(load)
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>推广分成</h1>
      </div>
    </div>

	<section class="card mb-6 p-5">
	  <div class="mb-4 flex flex-wrap items-center justify-between gap-3"><div><h2 class="text-sm font-semibold text-slate-200">现金分账设置</h2><p class="mt-1 text-xs text-slate-500">微信商家转账；场景 ID 与报备字段必须填写商户平台已审批的原值。</p></div><label class="flex items-center gap-2 text-sm"><input v-model="payoutConfig.enabled" type="checkbox" /> 启用现金提现</label></div>
	  <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
		<label><span class="label">微信支付渠道</span><select v-model.number="payoutConfig.wx_provider_id" class="input"><option :value="0">请选择</option><option v-for="provider in paymentProviders" :key="provider.id" :value="provider.id">{{ provider.name }}</option></select></label>
		<label><span class="label">转账场景 ID</span><input v-model.trim="payoutConfig.wx_transfer_scene_id" class="input font-mono" maxlength="36" placeholder="商户平台审批值" /></label>
		<label><span class="label">报备信息类型</span><input v-model.trim="payoutConfig.scene_report_info_type" class="input" maxlength="15" placeholder="场景规定字段" /></label>
		<label><span class="label">报备信息内容</span><input v-model.trim="payoutConfig.scene_report_info_content" class="input" maxlength="32" placeholder="真实业务内容" /></label>
		<label><span class="label">结算等待天数</span><input v-model.number="payoutConfig.settlement_days" class="input num" type="number" min="0" max="90" /></label>
		<label><span class="label">最低提现（分）</span><input v-model.number="payoutConfig.min_payout_minor" class="input num" type="number" min="1" /></label>
		<label><span class="label">单笔上限（分）</span><input v-model.number="payoutConfig.max_payout_minor" class="input num" type="number" min="1" /></label>
		<label><span class="label">每日总上限（分）</span><input v-model.number="payoutConfig.daily_payout_minor" class="input num" type="number" min="1" /></label>
		<label><span class="label">转账备注</span><input v-model.trim="payoutConfig.transfer_remark" class="input" maxlength="32" /></label>
		<label class="flex items-end gap-2 pb-2 text-sm"><input v-model="payoutConfig.require_review" type="checkbox" /> 每笔需管理员审核</label>
	  </div>
	  <div class="mt-4 flex justify-end"><button class="btn-primary" :disabled="payoutBusy" @click="savePayoutConfig">{{ payoutBusy ? '保存中…' : '保存分账设置' }}</button></div>
	</section>

	<section class="card mb-6 overflow-x-auto">
	  <div class="border-b border-slate-800 p-5"><h2 class="text-sm font-semibold text-slate-200">提现与转账</h2></div>
	  <table v-responsive-table class="table-base min-w-[980px]"><thead><tr><th>申请时间</th><th>用户</th><th>商户单号</th><th>收款账户</th><th>状态</th><th class="text-right">金额</th><th class="text-right">操作</th></tr></thead><tbody>
		<tr v-for="item in payouts" :key="item.id"><td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(item.requested_at).toLocaleString() }}</td><td class="text-xs">{{ item.user_email || `#${item.user_id}` }}</td><td><code class="font-mono text-xs">{{ item.out_bill_no }}</code></td><td class="font-mono text-xs">{{ item.openid_hint }}</td><td><span :class="item.status === 'SUCCESS' ? 'tag-green' : item.status === 'FAILED' || item.status === 'CANCELLED' ? 'tag-red' : 'tag-gray'">{{ payoutStatus(item.status) }}</span><p v-if="item.failure_message" class="mt-1 max-w-xs text-xs text-signal-red">{{ item.failure_message }}</p></td><td class="num text-right">{{ cashMoney(item.amount_minor, item.currency) }}</td><td class="whitespace-nowrap text-right"><button v-if="item.status === 'REVIEW_PENDING'" class="btn-primary !px-2.5 !py-1 text-xs" @click="payoutAction(item, 'approve')">审核打款</button><button v-if="item.status === 'REVIEW_PENDING'" class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="payoutAction(item, 'reject')">拒绝</button><button v-if="['SUBMITTING','PROCESSING','AWAITING_CONFIRMATION','STATUS_UNCERTAIN'].includes(item.status)" class="btn-ghost !px-2.5 !py-1 text-xs" @click="payoutAction(item, 'query')">查询微信</button></td></tr>
		<tr v-if="!payouts.length"><td colspan="7" class="py-10 text-center text-sm text-slate-500">暂无提现申请</td></tr>
	  </tbody></table>
	</section>

	<section class="card mb-6 p-5">
	  <div class="mb-4"><h2 class="text-sm font-semibold text-slate-200">微信收款账户</h2><p class="mt-1 text-xs text-slate-500">OpenID 加密保存，界面只显示掩码；必须属于所选商户 AppID。</p></div>
	  <div class="mb-4 grid gap-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)_auto]"><select v-model.number="accountUserID" class="input"><option v-for="user in users" :key="user.id" :value="user.id">{{ user.email }}</option></select><input v-model.trim="accountOpenID" class="input font-mono" maxlength="64" placeholder="管理员验证后的微信 OpenID" /><button class="btn-primary" :disabled="!accountUserID || accountOpenID.length < 8" @click="createPayoutAccount">绑定并验证</button></div>
	  <div class="space-y-2"><div v-for="account in payoutAccounts" :key="account.id" class="flex flex-wrap items-center gap-3 rounded-xl border border-slate-800 px-4 py-3 text-sm"><strong class="min-w-48 text-slate-200">{{ account.user_email || `用户 #${account.user_id}` }}</strong><code class="font-mono text-xs">{{ account.openid_hint }}</code><span :class="account.status === 'verified' ? 'tag-green' : account.status === 'disabled' ? 'tag-red' : 'tag-gray'">{{ account.status === 'verified' ? '已验证' : account.status === 'disabled' ? '已停用' : '待审核' }}</span><div class="ml-auto"><button v-if="account.status !== 'verified'" class="btn-ghost !px-2.5 !py-1 text-xs" @click="savePayoutAccount(account.user_id, 'verified', '管理员验证')">验证</button><button v-if="account.status !== 'disabled'" class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="savePayoutAccount(account.user_id, 'disabled', '管理员停用')">停用</button></div></div><p v-if="!payoutAccounts.length" class="py-6 text-center text-sm text-slate-500">暂无收款账户</p></div>
	</section>

    <section class="card mb-6 p-5">
      <h2 class="mb-4 text-sm font-semibold text-slate-200">创建推广码</h2>
      <div class="grid gap-3 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_140px_auto]">
        <label>
          <span class="label">推广用户</span>
          <select v-model.number="ownerUserID" class="input">
            <option v-for="user in users" :key="user.id" :value="user.id">{{ user.email }}</option>
          </select>
        </label>
        <label>
          <span class="label">自定义推广码（选填）</span>
          <input v-model.trim="customCode" class="input font-mono uppercase" maxlength="32" placeholder="留空自动生成" />
        </label>
        <label>
          <span class="label">佣金比例</span>
          <div class="relative"><input v-model.number="commissionPercent" class="input pr-8" type="number" min="5" max="10" step="0.25" /><span class="absolute right-3 top-2.5 text-xs text-slate-500">%</span></div>
        </label>
        <button class="btn-primary self-end" :disabled="busy || !ownerUserID || commissionPercent < 5 || commissionPercent > 10" @click="create">{{ busy ? '创建中…' : '创建' }}</button>
      </div>
    </section>

    <section class="card overflow-x-auto">
      <table v-responsive-table class="table-base min-w-[940px]">
        <thead><tr><th>推广码</th><th>推广用户</th><th>推广人数</th><th class="text-right">累计佣金</th><th>比例</th><th>状态</th><th>创建时间</th><th class="text-right">操作</th></tr></thead>
        <tbody>
          <tr v-for="item in items" :key="item.id">
            <td><code class="font-mono text-xs text-slate-200">{{ item.code }}</code></td>
            <td class="text-xs text-slate-300">{{ item.owner_email }}</td>
            <td class="num text-xs">{{ item.referred_users }}</td>
            <td class="num text-right text-xs text-signal-green">{{ formatMoney(item.commission_micro) }}</td>
            <td>
              <div class="relative w-28"><input v-model.number="item.commission_percent" class="input !py-1.5 pr-7 text-xs" type="number" min="5" max="10" step="0.25" /><span class="absolute right-2.5 top-2 text-[10px] text-slate-500">%</span></div>
            </td>
            <td><span :class="item.status === 'active' ? 'tag-green' : 'tag-gray'">{{ item.status === 'active' ? '生效中' : '已暂停' }}</span></td>
            <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(item.created_at).toLocaleDateString() }}</td>
            <td class="text-right">
              <button class="btn-ghost !px-2.5 !py-1 text-xs" :disabled="item.commission_percent < 5 || item.commission_percent > 10" @click="save(item)">保存</button>
              <button class="btn-ghost ml-2 !px-2.5 !py-1 text-xs" @click="toggle(item)">{{ item.status === 'active' ? '暂停' : '启用' }}</button>
              <button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="remove(item)">删除</button>
            </td>
          </tr>
          <tr v-if="!items.length"><td colspan="8" class="py-10 text-center text-sm text-slate-500">暂无推广码</td></tr>
        </tbody>
      </table>
    </section>
  </div>
</template>
