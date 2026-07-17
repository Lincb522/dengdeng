<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, copyText, withToast } from '../api/client'
import type { ReferralDashboard } from '../api/types'
import { formatMoney } from '../api/types'
import { useToast } from '../stores/toast'

const toast = useToast()
const data = ref<ReferralDashboard | null>(null)
const loading = ref(true)
const bindCode = ref('')

const primaryCode = computed(() => data.value?.codes[0] || null)
const referralLink = computed(() => primaryCode.value
  ? `${window.location.origin}/login?ref=${encodeURIComponent(primaryCode.value.code)}`
  : '')

async function load() {
  loading.value = true
  try {
    data.value = await api.get<ReferralDashboard>('/api/user/referrals')
  } finally {
    loading.value = false
  }
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
        <h1>推广中心 Referral</h1>
        <p class="mt-1 text-sm text-slate-500">佣金按被推广用户实际从余额扣除的 API 费用结算。</p>
      </div>
    </div>

    <div v-if="loading" class="card p-8 text-sm text-slate-500">正在读取…</div>
    <template v-else-if="data">
      <section class="mb-6 grid gap-4 md:grid-cols-3">
        <article class="card p-5">
          <div class="label">累计佣金 Commission</div>
          <div class="mt-2 num text-2xl font-semibold text-signal-green">{{ formatMoney(data.total_commission_micro) }}</div>
        </article>
        <article class="card p-5">
          <div class="label">推广用户 Referrals</div>
          <div class="mt-2 num text-2xl font-semibold text-slate-200">{{ primaryCode?.referred_users || 0 }}</div>
        </article>
        <article class="card p-5">
          <div class="label">当前比例 Rate</div>
          <div class="mt-2 num text-2xl font-semibold text-amber">{{ primaryCode ? `${(primaryCode.commission_bps / 100).toFixed(2)}%` : '—' }}</div>
        </article>
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
        <table class="table-base">
          <thead><tr><th>时间</th><th>用户</th><th>推广码</th><th class="text-right">用户消费</th><th class="text-right">比例</th><th class="text-right">佣金</th></tr></thead>
          <tbody>
            <tr v-for="item in data.commissions" :key="item.id">
              <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(item.created_at).toLocaleString() }}</td>
              <td class="text-xs text-slate-300">{{ item.referred_email || `#${item.referred_user_id}` }}</td>
              <td><span class="tag-gray font-mono">{{ item.code }}</span></td>
              <td class="num text-right text-xs">{{ formatMoney(item.base_cost_micro) }}</td>
              <td class="num text-right text-xs">{{ (item.commission_bps / 100).toFixed(2) }}%</td>
              <td class="num text-right text-xs text-signal-green">+{{ formatMoney(item.amount_micro) }}</td>
            </tr>
            <tr v-if="!data.commissions.length"><td colspan="6" class="py-10 text-center text-sm text-slate-500">暂无佣金记录</td></tr>
          </tbody>
        </table>
      </section>
    </template>
  </div>
</template>
