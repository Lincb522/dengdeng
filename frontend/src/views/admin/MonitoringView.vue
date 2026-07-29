<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, withToast } from '../../api/client'
import type { Group, OpsAccountHealth, OpsErrorLog, OpsRank, OpsSnapshot } from '../../api/types'
import { formatMoney, formatTokens, PLATFORM_LABELS } from '../../api/types'
import { summarizeProviderError } from '../../api/errors'
import OpsTrendChart from '../../components/OpsTrendChart.vue'
import ProviderLogo from '../../components/ProviderLogo.vue'
import ProviderSelect from '../../components/ProviderSelect.vue'
import ServerMonitorPanel from '../../components/ServerMonitorPanel.vue'

const snapshot = ref<OpsSnapshot | null>(null)
const errors = ref<OpsErrorLog[]>([])
const groups = ref<Group[]>([])
const initialQuery = new URLSearchParams(window.location.search)
const range = ref(initialQuery.get('range') || '24h')
const platform = ref(initialQuery.get('platform') || '')
const groupID = ref(initialQuery.get('group_id') || '')
const autoRefresh = ref(true)
const loading = ref(false)
const loadError = ref('')
const probingAll = ref(false)
const probingAccountID = ref<number | null>(null)
const refreshInterval = ref(30)
const refreshRemaining = ref(30)
const pageRoot = ref<HTMLElement | null>(null)
let refreshTimer: number | undefined
let countdownTimer: number | undefined

const overview = computed(() => snapshot.value?.overview)
const lastUpdated = computed(() => snapshot.value ? new Date(snapshot.value.generated_at).toLocaleString() : '—')
const visibleGroups = computed(() => groups.value.filter((group) => !platform.value || group.platform === platform.value))
const rankSections = computed(() => [
  { title: '模型分布', items: snapshot.value?.top_models ?? [], showProvider: true },
  { title: '分组负载', items: snapshot.value?.top_groups ?? [] },
  { title: '调用用户', items: snapshot.value?.top_users ?? [] },
])
const tokenSummary = computed(() => (snapshot.value?.model_usage ?? []).reduce((total, item) => ({
  input: total.input + item.input_tokens,
  output: total.output + item.output_tokens,
  cacheRead: total.cacheRead + item.cache_read_tokens,
  cacheWrite: total.cacheWrite + item.cache_write_5m_tokens + item.cache_write_1h_tokens,
}), { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }))
const monitorState = computed(() => {
  if (!overview.value) return { label: '等待数据', className: 'is-idle' }
  if (!overview.value.requests && !overview.value.account_available && !overview.value.account_attention && !overview.value.account_cooling) return { label: '等待接入', className: 'is-idle' }
  if (overview.value.health_score >= 90) return { label: '运行正常', className: 'is-healthy' }
  if (overview.value.health_score >= 70) return { label: '需要关注', className: 'is-warning' }
  return { label: '运行异常', className: 'is-critical' }
})

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const query = monitoringQuery()
    if (platform.value) query.set('platform', platform.value)
    if (groupID.value) query.set('group_id', groupID.value)
    const errorQuery = new URLSearchParams(query)
    errorQuery.set('page', '1'); errorQuery.set('size', '20')
    const [snapshotData, errorData] = await Promise.all([
      api.get<OpsSnapshot>(`/api/admin/ops/snapshot?${query}`),
      api.get<{ items: OpsErrorLog[] }>(`/api/admin/ops/errors?${errorQuery}`),
    ])
    snapshot.value = normalizeSnapshot(snapshotData)
    errors.value = errorData.items || []
    refreshRemaining.value = refreshInterval.value
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '暂时无法读取监控数据'
  } finally {
    loading.value = false
  }
}

function monitoringQuery() {
  const query = new URLSearchParams({ range: ['1h', '24h', '7d', '30d'].includes(range.value) ? range.value : '24h' })
  const durations: Record<string, number> = { '5m': 5 * 60_000, '30m': 30 * 60_000, '6h': 6 * 3_600_000 }
  if (durations[range.value]) { const end = new Date(); query.set('start', new Date(end.getTime() - durations[range.value]).toISOString()); query.set('end', end.toISOString()) }
  if (platform.value) query.set('platform', platform.value)
  if (groupID.value) query.set('group_id', groupID.value)
  const url = new URL(window.location.href); url.search = ''; url.searchParams.set('range', range.value); if (platform.value) url.searchParams.set('platform', platform.value); if (groupID.value) url.searchParams.set('group_id', groupID.value); window.history.replaceState({}, '', url)
  return query
}

function resetGroupWhenPlatformChanges() {
  const selected = groups.value.find((group) => String(group.id) === groupID.value)
  if (selected && platform.value && selected.platform !== platform.value) groupID.value = ''
}

function setRange(value: string) {
  range.value = value
}

function drilldown(item: { start: string; end: string }) { const query=new URLSearchParams({start:item.start,end:item.end});if(platform.value)query.set('platform',platform.value);if(groupID.value)query.set('group_id',groupID.value);window.location.href=`/admin/usage?${query}` }

async function toggleFullscreen() { if (!document.fullscreenElement) await pageRoot.value?.requestFullscreen(); else await document.exitFullscreen() }

async function resolveError(id: number) { await withToast(() => api.post(`/api/admin/ops/errors/${id}/resolve`, {}), '错误已标记处理'); await load() }

function percent(value: number) {
  return `${(value || 0).toFixed(2)}%`
}

function formatLatency(value: number) {
  if (!value) return '—'
  return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${Math.round(value)}ms`
}

function formatBytes(value: number) {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index > 1 ? 1 : 0)} ${units[index]}`
}

function bubbleStyle(values: number[], value: number) {
  const ratio = value / Math.max(...values, 1)
  return {
    opacity: String(value ? 0.5 + ratio * 0.5 : 0.28),
    transform: `scale(${value ? 0.58 + ratio * 0.42 : 0.46})`,
  }
}

function healthLabel(health: OpsAccountHealth['health']) {
  return { ready: '已验证', checking: '等待检测', stale: '检测过期', attention: '异常', cooling: '冷却中', disabled: '已停用' }[health]
}

function healthClass(health: OpsAccountHealth['health']) {
  return { ready: 'tag-green', checking: 'tag-cyan', stale: 'tag-amber', attention: 'tag-amber', cooling: 'tag-cyan', disabled: 'tag-gray' }[health]
}

function probeLabel(account: OpsAccountHealth) {
  if (!account.probe_checked_at) return '等待首次探测'
  return { healthy: account.probe_mode === 'transport' ? '传输可达' : '鉴权可用', degraded: '需处理', down: '不可达', expired: '凭据过期' }[account.probe_state] || '未知'
}

function probeClass(account: OpsAccountHealth) {
  return account.probe_state === 'healthy' ? 'text-signal-green' : account.probe_state ? 'text-amber' : 'text-slate-500'
}

function schedulerReasonLabel(reason: string) {
	return ({
		attempted: '本次已尝试',
		cooldown: '账号冷却',
		quota_exhausted: '额度用尽',
		model_cooldown: '模型临时冷却',
		concurrency_full: '并发已满',
		disabled: '账号停用',
		account_missing: '账号缺失',
		selection_exhausted: '调度已耗尽',
	} as Record<string, string>)[reason] || reason
}

function schedulerGroupName(groupId: number) {
	return groups.value.find((group) => group.id === groupId)?.name || `分组 #${groupId}`
}

function schedulerGroupPlatform(groupId: number) {
	return groups.value.find((group) => group.id === groupId)?.platform || ''
}

function modelPlatform(model?: string) {
	const normalized = (model || '').trim().toLowerCase().replace(/^models\//, '')
	if (normalized.startsWith('claude-')) return 'anthropic'
	if (normalized.startsWith('gemini-')) return 'gemini'
	if (normalized.startsWith('grok-')) return 'grok'
	if (/^(gpt-|o\d|codex-)/.test(normalized)) return 'openai'
	return platform.value
}

function namedPlatform(name?: string) {
	const normalized = (name || '').trim().toLowerCase()
	if (normalized === 'claude' || normalized === 'anthropic') return 'anthropic'
	if (normalized === 'google' || normalized === 'gemini') return 'gemini'
	if (normalized === 'xai' || normalized === 'grok') return 'grok'
	if (normalized === 'openai') return 'openai'
	return ''
}

async function triggerAllProbes() {
  probingAll.value = true
  try {
    await withToast(() => api.post('/api/admin/ops/probe', {}), '已开始检查全部上游账户')
    window.setTimeout(() => void load(), 900)
  } finally {
    probingAll.value = false
  }
}

async function probeAccount(id: number) {
  probingAccountID.value = id
  try {
    await withToast(() => api.post(`/api/admin/ops/accounts/${id}/probe`, {}), '账户检测已完成')
    await load()
  } finally {
    probingAccountID.value = null
  }
}

function rankErrorRate(rank: OpsRank) {
  return rank.requests ? (rank.error_requests / rank.requests) * 100 : 0
}

function normalizeSnapshot(data: OpsSnapshot): OpsSnapshot {
  const emptyWindow = { requests: 0, success_rate: 0, error_rate: 0, tokens: 0, cost_micro: 0, requests_per_minute: 0, requests_per_second: 0, tokens_per_second: 0, average_latency_ms: 0 }
  const realtime = data.realtime && typeof data.realtime === 'object'
    ? data.realtime
    : { captured_at: '', in_flight: 0, waiting: 0, last_minute: emptyWindow, breakdown: [] }
  return {
    ...data,
    trend: Array.isArray(data.trend) ? data.trend : [],
    top_models: Array.isArray(data.top_models) ? data.top_models : [],
    top_groups: Array.isArray(data.top_groups) ? data.top_groups : [],
    top_users: Array.isArray(data.top_users) ? data.top_users : [],
    top_accounts: Array.isArray(data.top_accounts) ? data.top_accounts : [],
    model_usage: Array.isArray(data.model_usage) ? data.model_usage : [],
    rate_profiles: Array.isArray(data.rate_profiles) ? data.rate_profiles : [],
    account_health: Array.isArray(data.account_health) ? data.account_health : [],
    recent_errors: Array.isArray(data.recent_errors) ? data.recent_errors : [],
    scheduler_diagnostics: Array.isArray(data.scheduler_diagnostics) ? data.scheduler_diagnostics : [],
		latency_histogram: Array.isArray(data.latency_histogram) ? data.latency_histogram : [],
		ttft_histogram: Array.isArray(data.ttft_histogram) ? data.ttft_histogram : [],
		error_trend: Array.isArray(data.error_trend) ? data.error_trend : [],
		status_distribution: Array.isArray(data.status_distribution) ? data.status_distribution : [],
		system_history: Array.isArray(data.system_history) ? data.system_history : [],
		job_heartbeats: Array.isArray(data.job_heartbeats) ? data.job_heartbeats : [],
    realtime: { ...realtime, last_minute: realtime.last_minute || emptyWindow, breakdown: Array.isArray(realtime.breakdown) ? realtime.breakdown : [] },
  }
}

function setupTimer() {
  if (refreshTimer) window.clearInterval(refreshTimer)
  refreshTimer = window.setInterval(() => {
    if (autoRefresh.value && !loading.value) void load()
  }, refreshInterval.value * 1000)
	if (countdownTimer) window.clearInterval(countdownTimer)
	countdownTimer = window.setInterval(() => { if (autoRefresh.value) refreshRemaining.value = refreshRemaining.value <= 1 ? refreshInterval.value : refreshRemaining.value - 1 }, 1000)
}

watch([range, platform, groupID], () => void load())
watch(platform, resetGroupWhenPlatformChanges)
watch(refreshInterval, () => { refreshRemaining.value = refreshInterval.value; setupTimer() })

onMounted(async () => {
  groups.value = await api.get<Group[]>('/api/admin/groups')
  await load()
  setupTimer()
})
onBeforeUnmount(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
	if (countdownTimer) window.clearInterval(countdownTimer)
})
</script>

<template>
  <div ref="pageRoot" class="ops-page ops-console">
    <header class="ops-console-head">
      <div class="ops-console-title">
        <span class="ops-console-state" :class="monitorState.className"><i></i>{{ monitorState.label }}</span>
        <h1>运行监控</h1>
      </div>
      <div class="ops-console-actions">
        <label class="ops-auto-refresh"><input v-model="autoRefresh" type="checkbox" /><span>{{ autoRefresh ? `${refreshRemaining}s` : '已暂停' }}</span></label>
        <select v-model.number="refreshInterval" class="input"><option :value="10">10 秒</option><option :value="30">30 秒</option><option :value="60">60 秒</option></select>
        <button class="btn-ghost" @click="toggleFullscreen">全屏</button>
        <button class="btn-ghost" :disabled="probingAll" @click="triggerAllProbes">{{ probingAll ? '检测中' : '检查上游' }}</button>
        <button class="btn-primary" :disabled="loading" @click="load">{{ loading ? '刷新中' : '刷新' }}</button>
      </div>
    </header>

    <section class="ops-console-filter" aria-label="监控筛选">
      <div class="ops-range-tabs" role="tablist" aria-label="时间范围">
        <button v-for="item in [{ value: '5m', label: '5 分钟' }, { value: '30m', label: '30 分钟' }, { value: '1h', label: '1 小时' }, { value: '6h', label: '6 小时' }, { value: '24h', label: '24 小时' }, { value: '7d', label: '7 天' }, { value: '30d', label: '30 天' }]" :key="item.value" type="button" :class="{ 'is-active': range === item.value }" @click="setRange(item.value)">{{ item.label }}</button>
      </div>
      <div class="ops-filter-selects">
        <ProviderSelect v-model="platform" include-all aria-label="监控平台筛选" />
        <select v-model="groupID" class="input"><option value="">全部分组</option><option v-for="group in visibleGroups" :key="group.id" :value="String(group.id)">{{ group.name }}</option></select>
      </div>
      <time class="ops-updated">{{ lastUpdated }}</time>
    </section>

    <div v-if="loadError" class="ops-error-state"><span>{{ loadError }}</span><button class="btn-ghost" @click="load">重试</button></div>

    <template v-else-if="overview">
      <section class="ops-status-strip" aria-label="运行概况">
        <div class="ops-status-primary" :class="monitorState.className">
          <span>健康度</span><strong>{{ overview.health_score }}</strong><small>/ 100</small>
        </div>
        <dl class="ops-status-metrics">
          <div><dt>请求</dt><dd>{{ overview.requests.toLocaleString() }}</dd><small>5 分钟 {{ overview.last_5_minutes.requests }}</small></div>
          <div><dt>成功率</dt><dd>{{ percent(overview.success_rate) }}</dd><small>失败 {{ overview.error_requests }}</small></div>
          <div><dt>P95 / TTFT</dt><dd>{{ formatLatency(overview.p95_latency_ms) }}</dd><small>{{ formatLatency(overview.p95_ttft_ms) }}</small></div>
          <div><dt>Token</dt><dd>{{ formatTokens(overview.total_tokens) }}</dd><small>5 分钟 {{ formatTokens(overview.last_5_minutes.tokens) }}</small></div>
          <div><dt>费用</dt><dd>{{ formatMoney(overview.cost_micro) }}</dd><small>5 分钟 {{ formatMoney(overview.last_5_minutes.cost_micro) }}</small></div>
          <div><dt>上游</dt><dd>{{ overview.account_available }}</dd><small>{{ overview.account_attention }} 异常 · {{ overview.account_cooling }} 冷却</small></div>
        </dl>
      </section>

      <section v-if="snapshot?.scheduler_diagnostics.length" class="ops-diagnostic-list" aria-label="调度诊断">
        <article v-for="diagnostic in snapshot.scheduler_diagnostics" :key="diagnostic.group_id" class="ops-diagnostic">
          <ProviderLogo :platform="schedulerGroupPlatform(diagnostic.group_id) || modelPlatform(diagnostic.model)" size="md" />
          <div><span>503 调度</span><strong>{{ schedulerGroupName(diagnostic.group_id) }}</strong><code v-if="diagnostic.model">{{ diagnostic.model }}</code></div>
          <div class="ops-diagnostic-reasons"><span v-for="(count, reason) in diagnostic.reasons" :key="reason">{{ schedulerReasonLabel(String(reason)) }} {{ count }}</span></div>
          <time>{{ new Date(diagnostic.updated_at).toLocaleString() }}</time>
        </article>
      </section>

      <section class="ops-command-grid">
        <OpsTrendChart :items="snapshot?.trend ?? []" @drilldown="drilldown" />
        <aside class="ops-live-console card">
          <header><h2>实时状态</h2><span class="ops-live-indicator"><i></i>LIVE</span></header>
          <dl class="ops-live-metrics">
            <div><dt>进行中</dt><dd>{{ snapshot?.realtime.in_flight ?? 0 }}</dd></div>
            <div><dt>等待</dt><dd>{{ snapshot?.realtime.waiting ?? 0 }}</dd></div>
            <div><dt>1 分钟请求</dt><dd>{{ snapshot?.realtime.last_minute.requests ?? 0 }}</dd></div>
            <div><dt>QPS</dt><dd>{{ (snapshot?.realtime.last_minute.requests_per_second ?? 0).toFixed(2) }}</dd></div>
            <div><dt>TPS</dt><dd>{{ (snapshot?.realtime.last_minute.tokens_per_second ?? 0).toFixed(1) }}</dd></div>
            <div><dt>切换账号</dt><dd>{{ overview.switch_count || 0 }}</dd></div>
          </dl>
          <div class="ops-pool-state">
            <div><span>已验证</span><strong class="text-signal-green">{{ overview.account_available }}</strong></div>
            <div><span>待关注</span><strong class="text-amber">{{ overview.account_attention }}</strong></div>
            <div><span>冷却中</span><strong class="text-signal-cyan">{{ overview.account_cooling }}</strong></div>
            <div><span>已停用</span><strong>{{ overview.account_disabled }}</strong></div>
          </div>
          <RouterLink to="/admin/accounts" class="ops-console-link">管理上游账号 →</RouterLink>
        </aside>
      </section>

      <ServerMonitorPanel v-if="snapshot?.system" :system="snapshot.system" :history="snapshot.system_history" />

      <section class="ops-console-section">
        <header><h2>链路观测</h2><span>{{ snapshot?.query_mode || 'raw' }}</span></header>
        <div class="ops-observe-grid">
          <article class="ops-observe-panel">
            <h3>响应分位</h3>
            <div class="ops-latency-matrix">
              <dl><dt>总耗时</dt><div><span>P50</span><strong>{{ formatLatency(overview.p50_latency_ms) }}</strong></div><div><span>P90</span><strong>{{ formatLatency(overview.p90_latency_ms) }}</strong></div><div><span>P95</span><strong>{{ formatLatency(overview.p95_latency_ms) }}</strong></div><div><span>P99</span><strong>{{ formatLatency(overview.p99_latency_ms) }}</strong></div></dl>
              <dl><dt>首字耗时</dt><div><span>P50</span><strong>{{ formatLatency(overview.p50_ttft_ms) }}</strong></div><div><span>P90</span><strong>{{ formatLatency(overview.p90_ttft_ms) }}</strong></div><div><span>P95</span><strong>{{ formatLatency(overview.p95_ttft_ms) }}</strong></div><div><span>P99</span><strong>{{ formatLatency(overview.p99_ttft_ms) }}</strong></div></dl>
            </div>
          </article>
          <article class="ops-observe-panel">
            <h3>吞吐</h3>
            <dl class="ops-throughput-matrix">
              <div><dt>QPS</dt><dd>{{ overview.qps?.current?.toFixed(3) || '0.000' }}</dd><small>峰值 {{ overview.qps?.peak?.toFixed(3) || '0.000' }}</small></div>
              <div><dt>TPS</dt><dd>{{ overview.tps?.current?.toFixed(1) || '0.0' }}</dd><small>峰值 {{ overview.tps?.peak?.toFixed(1) || '0.0' }}</small></div>
              <div><dt>切换率</dt><dd>{{ percent(overview.switch_rate || 0) }}</dd><small>{{ overview.switch_count || 0 }} 次</small></div>
            </dl>
          </article>
          <article class="ops-observe-panel">
            <h3>Token 构成</h3>
            <dl class="ops-token-matrix">
              <div><dt>输入</dt><dd>{{ formatTokens(tokenSummary.input) }}</dd></div>
              <div><dt>输出</dt><dd>{{ formatTokens(tokenSummary.output) }}</dd></div>
              <div><dt>缓存读</dt><dd>{{ formatTokens(tokenSummary.cacheRead) }}</dd></div>
              <div><dt>缓存写</dt><dd>{{ formatTokens(tokenSummary.cacheWrite) }}</dd></div>
            </dl>
          </article>
        </div>
      </section>

      <section class="ops-console-section">
        <header><h2>分布与队列</h2></header>
        <div class="ops-distribution-grid">
          <article class="ops-observe-panel"><h3>总耗时</h3><div class="ops-histogram"><div v-for="bucket in snapshot?.latency_histogram" :key="bucket.range"><span>{{ bucket.range }}</span><i class="ops-density-dot" :style="bubbleStyle(snapshot?.latency_histogram.map((item) => item.count) || [], bucket.count)"></i><strong>{{ bucket.count }}</strong></div></div></article>
          <article class="ops-observe-panel"><h3>首字耗时</h3><div class="ops-histogram"><div v-for="bucket in snapshot?.ttft_histogram" :key="bucket.range"><span>{{ bucket.range }}</span><i class="ops-density-dot" :style="bubbleStyle(snapshot?.ttft_histogram.map((item) => item.count) || [], bucket.count)"></i><strong>{{ bucket.count }}</strong></div></div></article>
          <article class="ops-observe-panel"><h3>错误状态</h3><div class="ops-status-list"><div v-for="item in snapshot?.status_distribution" :key="item.status_code"><span :class="item.status_code >= 500 ? 'tag-red' : 'tag-amber'">HTTP {{ item.status_code }}</span><i class="ops-density-dot is-error" :style="bubbleStyle(snapshot?.status_distribution.map((row) => row.count) || [], item.count)"></i><strong>{{ item.count }}</strong></div><div v-if="!snapshot?.status_distribution.length" class="ops-empty">无错误</div></div></article>
          <article class="ops-observe-panel"><h3>后台任务</h3><div class="ops-job-list"><div v-for="job in snapshot?.job_heartbeats" :key="job.job_name"><span>{{ job.job_name }}</span><strong :class="job.last_error ? 'text-signal-red' : 'text-signal-green'">{{ job.last_error ? '失败' : '正常' }}</strong><small>{{ job.last_success_at ? new Date(job.last_success_at).toLocaleString() : '尚未成功' }}</small></div><div v-if="!snapshot?.job_heartbeats.length" class="ops-job-empty">暂无任务</div></div></article>
        </div>
      </section>

      <section class="ops-console-section">
        <header><h2>实时并发</h2></header>
        <div class="ops-concurrency-grid"><article v-for="item in snapshot?.realtime.breakdown" :key="`capacity-${item.scope}-${item.id || item.name}`"><header><ProviderLogo v-if="item.scope === 'platform'" :platform="namedPlatform(item.name)" size="sm" /><span class="ops-capacity-scope">{{ ({platform:'平台',group:'分组',account:'账号',user:'用户'} as Record<string,string>)[item.scope] }}</span><strong :title="item.name">{{ item.name }}</strong></header><div class="ops-capacity-value"><b>{{ item.in_flight }}</b><span>/ {{ item.max_capacity || '不限' }}</span></div><div class="ops-capacity-track"><i :style="{ width: `${Math.min(item.load_percentage || 0, 100)}%` }"></i></div><footer><span>负载 {{ item.max_capacity ? percent(item.load_percentage) : '不限' }}</span><span>等待 {{ item.waiting || 0 }}</span></footer></article></div>
      </section>

      <p v-if="snapshot?.sample_truncated" class="ops-sample-note">明细超过 50,000 条，趋势与排行按最近样本计算。</p>

      <section class="ops-rank-grid">
        <article v-for="section in rankSections" :key="section.title" class="ops-rank-panel card">
          <div class="ops-section-title"><h3>{{ section.title }}</h3></div>
          <div v-if="section.items.length" class="ops-rank-list">
            <div v-for="(rank, index) in section.items" :key="`${rank.id || rank.name}-${index}`" class="ops-rank-row" :class="{ 'has-provider': section.showProvider }"><span class="ops-rank-index">{{ String(index + 1).padStart(2, '0') }}</span><ProviderLogo v-if="section.showProvider" :platform="modelPlatform(rank.name)" size="sm" /><div class="min-w-0"><strong :title="rank.name">{{ rank.name }}</strong><small>{{ rank.requests.toLocaleString() }} 次 · {{ formatTokens(rank.tokens) }}</small></div><div class="text-right"><b>{{ percent(rankErrorRate(rank)) }}</b><small>失败率</small></div></div>
          </div>
          <div v-else class="ops-empty">暂无调用</div>
        </article>
      </section>

      <section class="ops-detail-stack">
        <article class="card overflow-hidden">
          <div class="ops-section-title ops-table-title"><h3>模型用量明细</h3></div>
          <div class="overflow-x-auto">
            <table v-responsive-table class="table-base ops-model-table">
              <thead><tr><th>模型</th><th class="text-right">请求</th><th class="text-right">输入</th><th class="text-right">输出</th><th class="text-right">缓存读</th><th class="text-right">5m 写入</th><th class="text-right">1h 写入</th><th class="text-right">失败率</th><th class="text-right">平均 TTFT</th><th class="text-right">平均 TPS</th><th class="text-right">平均总耗时</th><th class="text-right">费用</th></tr></thead>
              <tbody>
                <tr v-for="item in snapshot?.model_usage" :key="item.name">
                  <td class="font-mono text-xs text-slate-200"><span class="provider-model-name"><ProviderLogo :platform="modelPlatform(item.name)" size="sm" /><span>{{ item.name }}</span></span></td>
                  <td class="num text-right">{{ item.requests.toLocaleString() }}</td>
                  <td class="num text-right">{{ formatTokens(item.input_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.output_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.cache_read_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.cache_write_5m_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.cache_write_1h_tokens) }}</td>
                  <td class="num text-right" :class="rankErrorRate(item) ? 'text-amber' : 'text-signal-green'">{{ percent(rankErrorRate(item)) }}</td>
					<td class="num text-right">{{ formatLatency(item.average_ttft_ms) }}<small class="ml-1 text-[10px] text-slate-500">{{ item.ttft_samples }}</small></td>
					<td class="num text-right">{{ (item.output_tps || 0).toFixed(1) }}</td>
                  <td class="num text-right">{{ formatLatency(item.average_latency_ms) }}</td>
                  <td class="num text-right text-slate-200">{{ formatMoney(item.cost_micro) }}</td>
                </tr>
                <tr v-if="!snapshot?.model_usage.length"><td colspan="12" class="py-10 text-center text-sm text-slate-500">当前时间段还没有模型调用</td></tr>
              </tbody>
            </table>
          </div>
        </article>

        <article class="card overflow-hidden">
          <div class="ops-section-title ops-table-title"><h3>当前倍率配置</h3><RouterLink to="/admin/groups" class="ops-link">管理倍率</RouterLink></div>
          <div class="overflow-x-auto">
            <table v-responsive-table class="table-base ops-rate-table">
              <thead><tr><th>分组</th><th>平台</th><th class="text-right">文本</th><th class="text-right">缓存读</th><th class="text-right">5m 缓存写</th><th class="text-right">1h 缓存写</th><th class="text-right">生图</th></tr></thead>
              <tbody>
                <tr v-for="profile in snapshot?.rate_profiles" :key="profile.id">
                  <td class="font-medium text-slate-200">{{ profile.name }}</td>
                  <td><span class="tag-gray provider-inline-label"><ProviderLogo :platform="profile.platform" size="sm" />{{ PLATFORM_LABELS[profile.platform] || profile.platform }}</span></td>
                  <td class="num text-right">×{{ profile.rate_multiplier }}</td>
                  <td class="num text-right">×{{ profile.cache_read_multiplier }}</td>
                  <td class="num text-right">×{{ profile.cache_write_5m_multiplier }}</td>
                  <td class="num text-right">×{{ profile.cache_write_1h_multiplier }}</td>
                  <td class="num text-right">{{ profile.image_rate_independent ? `×${profile.image_rate_multiplier}` : '跟随文本' }}</td>
                </tr>
                <tr v-if="!snapshot?.rate_profiles.length"><td colspan="7" class="py-10 text-center text-sm text-slate-500">当前筛选下没有分组配置</td></tr>
              </tbody>
            </table>
          </div>
        </article>
      </section>

      <section class="ops-detail-stack">
        <article class="card overflow-hidden">
          <div class="ops-section-title ops-table-title"><h3>账号状态</h3></div>
          <div class="overflow-x-auto">
            <table v-responsive-table class="table-base ops-account-table">
              <thead><tr><th>账号</th><th>分组</th><th>状态</th><th>最近探测</th><th>探测结果</th><th class="text-right">错误次数</th><th>操作</th></tr></thead>
              <tbody>
                <tr v-for="account in snapshot?.account_health" :key="account.id">
                  <td><div class="provider-cell"><ProviderLogo :platform="account.platform" size="md" /><div><div class="font-medium text-slate-200">{{ account.name }}</div><div class="text-xs text-slate-500">{{ account.email || PLATFORM_LABELS[account.platform] || account.platform }}</div></div></div></td>
                  <td class="text-xs text-slate-400">{{ account.group_name || '—' }}</td>
                  <td><span :class="healthClass(account.health)">{{ healthLabel(account.health) }}</span></td>
                  <td class="whitespace-nowrap text-xs text-slate-500">{{ account.probe_checked_at ? new Date(account.probe_checked_at).toLocaleString() : '尚未检测' }}</td>
                  <td class="max-w-xs text-xs" :class="probeClass(account)" :title="account.probe_error || account.last_error">
                    <span>{{ probeLabel(account) }}</span>
                    <small v-if="account.probe_latency_ms" class="ml-1 text-slate-500">{{ formatLatency(account.probe_latency_ms) }}</small>
                  </td>
                  <td class="num text-right" :class="account.error_count ? 'text-amber' : 'text-slate-500'">{{ account.error_count }}</td>
                  <td><button class="btn-ghost !px-2 !py-1 text-xs" :disabled="probingAccountID === account.id" @click="probeAccount(account.id)">{{ probingAccountID === account.id ? '检测中' : '检测' }}</button></td>
                </tr>
                <tr v-if="!snapshot?.account_health.length"><td colspan="7" class="py-10 text-center text-sm text-slate-500">当前筛选下没有上游账号</td></tr>
              </tbody>
            </table>
          </div>
        </article>

        <article class="card overflow-hidden">
          <div class="ops-section-title ops-table-title"><h3>错误中心</h3><RouterLink to="/admin/usage?status=error" class="ops-link">请求明细</RouterLink></div>
          <div class="overflow-x-auto">
            <table v-responsive-table class="table-base ops-error-table">
              <thead><tr><th>时间</th><th>用户 / 密钥</th><th>模型 / 端点</th><th>分组 / 账号</th><th>请求 IP / 地区</th><th>错误</th><th>请求编号</th><th class="text-right">耗时</th><th>处理</th></tr></thead>
              <tbody>
                <tr v-for="item in errors" :key="item.id">
                  <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(item.created_at).toLocaleString() }}</td>
                  <td><div class="text-xs text-slate-300">{{ item.user_email || '—' }}</div><div class="text-[11px] text-slate-500">{{ item.key_name || '未命名密钥' }}</div></td>
                  <td><div class="font-mono text-xs text-slate-200">{{ item.model || '—' }}</div><div class="font-mono text-[10px] text-slate-500">{{ item.request_path || '—' }}</div></td>
                  <td><div class="text-xs text-slate-400">{{ item.group_name || '—' }}</div><div class="text-[10px] text-slate-500">{{ item.account_name || '未选中账号' }}</div></td>
                  <td><div class="font-mono text-xs text-slate-300">{{ item.client_ip || '—' }}</div><div class="text-[10px] text-slate-500">{{ item.ip_location || '地区解析中' }}</div></td>
                  <td class="max-w-sm truncate text-xs text-signal-red" :title="item.error_message"><span class="mr-1 font-mono">{{ item.status_code }}</span>{{ summarizeProviderError(item.error_message || '上游返回失败') }}</td>
                  <td class="font-mono text-[10px] text-slate-500">{{ item.request_id || '—' }}</td>
                  <td class="num text-right text-xs text-slate-500">{{ formatLatency(item.duration_ms) }}</td>
                  <td><span v-if="item.resolved_at" class="tag-green">已处理</span><button v-else class="btn-ghost !px-2 !py-1 text-xs" @click="resolveError(item.id)">标记处理</button></td>
                </tr>
                <tr v-if="!errors.length"><td colspan="9" class="py-10 text-center text-sm text-slate-500">当前时间段没有失败记录</td></tr>
              </tbody>
            </table>
          </div>
        </article>
      </section>
    </template>

    <div v-else class="ops-loading-state">正在读取运行数据…</div>
  </div>
</template>
