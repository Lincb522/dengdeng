<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { ReferralCodeStats, User } from '../../api/types'
import { formatMoney } from '../../api/types'

type ReferralRow = ReferralCodeStats & { commission_percent: number }

const items = ref<ReferralRow[]>([])
const users = ref<User[]>([])
const ownerUserID = ref<number | null>(null)
const customCode = ref('')
const commissionPercent = ref(5)
const busy = ref(false)

async function load() {
  const [codes, userList] = await Promise.all([
    api.get<ReferralCodeStats[]>('/api/admin/referral-codes'),
    api.get<User[]>('/api/admin/users'),
  ])
  items.value = (Array.isArray(codes) ? codes : []).map((item) => ({ ...item, commission_percent: item.commission_bps / 100 }))
  users.value = Array.isArray(userList) ? userList : []
  if (!ownerUserID.value && users.value.length) ownerUserID.value = users.value[0].id
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
        <h1>推广分成 Referrals</h1>
        <p class="mt-1 text-sm text-slate-500">佣金按实际余额消费结算，比例只能设置为 5%–10%。</p>
      </div>
    </div>

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
      <table class="table-base min-w-[940px]">
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
