<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { AlertEvent, AlertRule, ChannelProbe, Group, UpstreamAccount } from '../../api/types'
import { PLATFORM_LABELS } from '../../api/types'
import { summarizeProviderError } from '../../api/errors'

const tab = ref<'alerts' | 'history'>('alerts')
const rules = ref<AlertRule[]>([])
const events = ref<AlertEvent[]>([])
const probes = ref<ChannelProbe[]>([])
const groups = ref<Group[]>([])
const accounts = ref<UpstreamAccount[]>([])
const loading = ref(true)
const saving = ref(false)
const showEditor = ref(false)
const editingID = ref<number | null>(null)
const eventFilter = ref<'open' | 'resolved' | ''>('open')
const hours = ref(24)
const historyAccountID = ref('')
const form = ref({ name: '', enabled: true, condition: 'down' as AlertRule['condition'], platform: '', group_id: 0, account_id: 0, notify_email: '' })

const visibleGroups = computed(() => groups.value.filter((group) => !form.value.platform || group.platform === form.value.platform))
const visibleAccounts = computed(() => accounts.value.filter((account) => !form.value.platform || account.platform === form.value.platform))

function formatTime(value: string | null) { return value ? new Date(value).toLocaleString() : '—' }
function formatLatency(value: number) { return value ? (value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${value}ms`) : '—' }
function stateLabel(state: string) { return ({ open: '处理中', resolved: '已恢复', healthy: '正常', degraded: '需关注', down: '不可用', expired: '已过期' } as Record<string, string>)[state] || state }

async function loadRulesAndEvents() {
  const query = eventFilter.value ? `?state=${eventFilter.value}&limit=120` : '?limit=120'
  const [ruleData, eventData] = await Promise.all([
    api.get<AlertRule[]>('/api/admin/alerts/rules'),
    api.get<{ items: AlertEvent[] }>(`/api/admin/alerts/events${query}`),
  ])
  rules.value = ruleData
  events.value = eventData.items
}

async function loadHistory() {
  const query = new URLSearchParams({ hours: String(hours.value), limit: '300' })
  if (historyAccountID.value) query.set('account_id', historyAccountID.value)
  const data = await api.get<{ items: ChannelProbe[] }>(`/api/admin/channel-monitor/history?${query}`)
  probes.value = data.items
}

async function load() {
  loading.value = true
  try {
    const [groupData, accountData] = await Promise.all([
      api.get<Group[]>('/api/admin/groups'),
      api.get<UpstreamAccount[]>('/api/admin/accounts'),
    ])
    groups.value = groupData
    accounts.value = accountData
    await Promise.all([loadRulesAndEvents(), loadHistory()])
  } finally { loading.value = false }
}

function resetEditor() {
  editingID.value = null
  form.value = { name: '', enabled: true, condition: 'down', platform: '', group_id: 0, account_id: 0, notify_email: '' }
}

function openCreate() { resetEditor(); showEditor.value = true }
function openEdit(rule: AlertRule) {
  editingID.value = rule.id
  form.value = { name: rule.name, enabled: rule.enabled, condition: rule.condition, platform: rule.platform || '', group_id: rule.group_id, account_id: rule.account_id, notify_email: rule.notify_email || '' }
  showEditor.value = true
}

async function saveRule() {
  if (!form.value.name.trim()) return
  saving.value = true
  try {
    const payload = { ...form.value, platform: form.value.platform || '', group_id: Number(form.value.group_id) || 0, account_id: Number(form.value.account_id) || 0 }
    const result = editingID.value
      ? await withToast(() => api.put(`/api/admin/alerts/rules/${editingID.value}`, payload), '告警规则已保存')
      : await withToast(() => api.post('/api/admin/alerts/rules', payload), '告警规则已创建')
    if (result !== null) { showEditor.value = false; await loadRulesAndEvents() }
  } finally { saving.value = false }
}

async function removeRule(rule: AlertRule) {
  if (!confirm(`删除告警规则「${rule.name}」？已有事件记录会保留。`)) return
  const done = await withToast(() => api.delete(`/api/admin/alerts/rules/${rule.id}`), '告警规则已删除')
  if (done !== null) await loadRulesAndEvents()
}

async function toggleRule(rule: AlertRule) {
  const saved = await withToast(() => api.put(`/api/admin/alerts/rules/${rule.id}`, { ...rule, enabled: !rule.enabled }), rule.enabled ? '规则已暂停' : '规则已启用')
  if (saved !== null) await loadRulesAndEvents()
}

async function acknowledge(event: AlertEvent) {
  const done = await withToast(() => api.post(`/api/admin/alerts/events/${event.id}/acknowledge`, {}), '已确认该告警')
  if (done !== null) await loadRulesAndEvents()
}

async function changeTab(value: 'alerts' | 'history') { tab.value = value; if (value === 'history') await loadHistory() }

onMounted(load)
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>告警与巡检</h1>
        <p class="mt-1 text-sm text-slate-500">告警由非计费健康探测驱动：同一故障会持续更新，恢复后自动关闭，不会重复刷屏。</p>
      </div>
      <div class="flex gap-2"><button class="btn-ghost" :disabled="loading" @click="load">刷新</button><button class="btn-primary" @click="openCreate">新建规则</button></div>
    </div>

    <div class="mb-5 flex gap-2 border-b border-slate-800">
      <button class="border-b-2 px-3 py-2 text-sm" :class="tab === 'alerts' ? 'border-amber text-amber' : 'border-transparent text-slate-500'" @click="changeTab('alerts')">告警中心</button>
      <button class="border-b-2 px-3 py-2 text-sm" :class="tab === 'history' ? 'border-amber text-amber' : 'border-transparent text-slate-500'" @click="changeTab('history')">频道巡检历史</button>
    </div>

    <template v-if="tab === 'alerts'">
      <section class="card mb-5 overflow-x-auto">
        <div class="flex items-center justify-between gap-3 border-b border-slate-800 px-5 py-4"><div><h2 class="text-sm font-semibold text-slate-100">告警规则</h2><p class="mt-1 text-xs text-slate-500">可按平台、分组或单个上游账号收窄范围。填写邮箱时，SMTP 已配置会发送首次告警邮件。</p></div></div>
        <table v-responsive-table class="table-base min-w-[760px]"><thead><tr><th>规则</th><th>触发条件</th><th>范围</th><th>通知</th><th>状态</th><th class="text-right">操作</th></tr></thead>
          <tbody><tr v-for="rule in rules" :key="rule.id"><td class="font-medium text-slate-200">{{ rule.name }}</td><td><span class="tag-gray">{{ rule.condition === 'down' ? '仅不可用' : rule.condition === 'degraded_or_down' ? '降级或不可用' : '非健康状态' }}</span></td><td class="text-xs text-slate-400">{{ rule.account_id ? `账号 #${rule.account_id}` : rule.group_id ? `分组 #${rule.group_id}` : rule.platform ? (PLATFORM_LABELS[rule.platform] || rule.platform) : '全部账号' }}</td><td class="text-xs text-slate-500">{{ rule.notify_email || '管理邮箱 / 控制台' }}</td><td><span :class="rule.enabled ? 'tag-green' : 'tag-gray'">{{ rule.enabled ? '启用' : '暂停' }}</span></td><td class="space-x-2 text-right"><button class="btn-ghost !px-2.5 !py-1 text-xs" @click="toggleRule(rule)">{{ rule.enabled ? '暂停' : '启用' }}</button><button class="btn-ghost !px-2.5 !py-1 text-xs" @click="openEdit(rule)">编辑</button><button class="btn-danger !px-2.5 !py-1 text-xs" @click="removeRule(rule)">删除</button></td></tr><tr v-if="!rules.length"><td colspan="6" class="py-10 text-center text-sm text-slate-500">还没有规则。默认的“上游账号不可用”规则会在服务启动后自动创建。</td></tr></tbody>
        </table>
      </section>

      <section class="card overflow-x-auto">
        <div class="flex items-center justify-between gap-3 border-b border-slate-800 px-5 py-4"><div><h2 class="text-sm font-semibold text-slate-100">告警事件</h2><p class="mt-1 text-xs text-slate-500">确认表示已看到；只有后续健康探测恢复才会自动关闭事件。</p></div><select v-model="eventFilter" class="input w-32 text-xs" @change="loadRulesAndEvents"><option value="open">处理中</option><option value="resolved">已恢复</option><option value="">全部</option></select></div>
        <table v-responsive-table class="table-base min-w-[800px]"><thead><tr><th>事件</th><th>账号</th><th>首次 / 最近</th><th>投递</th><th>状态</th><th class="text-right">操作</th></tr></thead>
			<tbody><tr v-for="event in events" :key="event.id"><td><div class="font-medium" :class="event.severity === 'critical' ? 'text-signal-red' : 'text-amber'">{{ event.title }}</div><p class="mt-1 max-w-md truncate text-xs text-slate-500" :title="event.message">{{ summarizeProviderError(event.message) }}</p></td><td class="text-xs text-slate-400">{{ event.account_name || `#${event.account_id}` }}</td><td class="whitespace-nowrap text-xs text-slate-500">{{ formatTime(event.first_seen_at) }}<br />{{ formatTime(event.last_seen_at) }}</td><td class="text-xs"><span :class="event.delivery_status === 'sent' ? 'text-signal-green' : event.delivery_status === 'failed' ? 'text-signal-red' : 'text-slate-500'">{{ event.delivery_status === 'sent' ? '邮件已发送' : event.delivery_status === 'failed' ? '投递失败' : '控制台内' }}</span><p v-if="event.delivery_error" class="mt-1 max-w-40 truncate text-signal-red" :title="event.delivery_error">{{ summarizeProviderError(event.delivery_error) }}</p></td><td><span :class="event.state === 'open' ? 'tag-amber' : 'tag-green'">{{ stateLabel(event.state) }}</span><small v-if="event.acknowledged_at" class="ml-1 text-xs text-slate-500">已确认</small></td><td class="text-right"><button v-if="event.state === 'open' && !event.acknowledged_at" class="btn-ghost !px-2.5 !py-1 text-xs" @click="acknowledge(event)">确认</button><span v-else class="text-xs text-slate-500">—</span></td></tr><tr v-if="!events.length"><td colspan="6" class="py-10 text-center text-sm text-slate-500">当前没有匹配的告警事件。</td></tr></tbody>
        </table>
      </section>
    </template>

    <template v-else>
      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-end justify-between gap-3 border-b border-slate-800 px-5 py-4"><div><h2 class="text-sm font-semibold text-slate-100">频道巡检历史</h2><p class="mt-1 text-xs text-slate-500">API Key 检查模型列表；OAuth 仅检查令牌期限与传输链路，不会生成内容。</p></div><div class="flex gap-2"><select v-model.number="hours" class="input w-28 text-xs" @change="loadHistory"><option :value="1">近 1 小时</option><option :value="24">近 24 小时</option><option :value="168">近 7 天</option><option :value="720">近 30 天</option></select><select v-model="historyAccountID" class="input w-44 text-xs" @change="loadHistory"><option value="">全部账号</option><option v-for="account in accounts" :key="account.id" :value="String(account.id)">{{ account.name }}</option></select></div></div>
		<div class="overflow-x-auto"><table v-responsive-table class="table-base min-w-[760px]"><thead><tr><th>时间</th><th>账号 / 分组</th><th>结果</th><th>方式</th><th>延迟</th><th>说明</th></tr></thead><tbody><tr v-for="probe in probes" :key="probe.id"><td class="whitespace-nowrap text-xs text-slate-500">{{ formatTime(probe.checked_at) }}</td><td><div class="font-medium text-slate-200">{{ probe.account_name || `账号 #${probe.account_id}` }}</div><small class="text-slate-500">{{ probe.group_name || PLATFORM_LABELS[probe.platform] || probe.platform }}</small></td><td><span :class="probe.state === 'healthy' ? 'tag-green' : probe.state === 'degraded' ? 'tag-amber' : 'tag-red'">{{ stateLabel(probe.state) }}</span><small v-if="probe.status_code" class="ml-1 text-xs text-slate-500">{{ probe.status_code }}</small></td><td class="text-xs text-slate-400">{{ probe.mode === 'transport' ? '传输检查' : '接口检查' }}</td><td class="num text-xs text-slate-400">{{ formatLatency(probe.latency_ms) }}</td><td class="max-w-md truncate text-xs" :class="probe.error_message ? 'text-signal-red' : 'text-slate-500'" :title="probe.error_message">{{ probe.error_message ? summarizeProviderError(probe.error_message) : '—' }}</td></tr><tr v-if="!probes.length"><td colspan="6" class="py-10 text-center text-sm text-slate-500">这一时间段没有巡检记录。</td></tr></tbody></table></div>
      </section>
    </template>

    <Teleport to="body"><div v-if="showEditor" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="showEditor = false"><div class="card w-full max-w-lg p-6"><h3 class="mb-1 text-base font-semibold text-slate-100">{{ editingID ? '编辑告警规则' : '新建告警规则' }}</h3><p class="mb-5 text-xs text-slate-500">规则由账号健康巡检触发，不会向上游发送生成请求。</p><div class="space-y-4"><label><span class="label">名称</span><input v-model.trim="form.name" class="input" maxlength="120" placeholder="例如：OpenAI 主池不可用" /></label><div class="grid grid-cols-2 gap-3"><label><span class="label">触发条件</span><select v-model="form.condition" class="input"><option value="down">仅不可用</option><option value="degraded_or_down">降级或不可用</option><option value="not_healthy">任何非健康状态</option></select></label><label><span class="label">平台范围</span><select v-model="form.platform" class="input"><option value="">全部平台</option><option value="anthropic">Claude</option><option value="openai">OpenAI</option><option value="gemini">Gemini</option></select></label></div><div class="grid grid-cols-2 gap-3"><label><span class="label">分组范围</span><select v-model.number="form.group_id" class="input"><option :value="0">全部分组</option><option v-for="group in visibleGroups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label><label><span class="label">账号范围</span><select v-model.number="form.account_id" class="input"><option :value="0">全部账号</option><option v-for="account in visibleAccounts" :key="account.id" :value="account.id">{{ account.name }}</option></select></label></div><label><span class="label">通知邮箱（可选）</span><input v-model.trim="form.notify_email" type="email" class="input" placeholder="留空使用管理邮箱；SMTP 未配置时仅控制台告警" /></label><label class="flex items-center gap-2 text-sm text-slate-300"><input v-model="form.enabled" type="checkbox" class="accent-amber" /> 启用规则</label><div class="flex justify-end gap-3 pt-2"><button class="btn-ghost" @click="showEditor = false">取消</button><button class="btn-primary" :disabled="saving || !form.name" @click="saveRule">{{ saving ? '保存中…' : '保存规则' }}</button></div></div></div></div></Teleport>
  </div>
</template>
