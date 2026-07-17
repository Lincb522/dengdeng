<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, withToast } from '../../api/client'
import type { Group, OpsAccountHealth, OpsRank, OpsSnapshot } from '../../api/types'
import { formatMoney, formatTokens, PLATFORM_LABELS } from '../../api/types'
import { summarizeProviderError } from '../../api/errors'
import OpsTrendChart from '../../components/OpsTrendChart.vue'

const snapshot = ref<OpsSnapshot | null>(null)
const groups = ref<Group[]>([])
const range = ref('24h')
const platform = ref('')
const groupID = ref('')
const autoRefresh = ref(true)
const loading = ref(false)
const loadError = ref('')
const probingAll = ref(false)
const probingAccountID = ref<number | null>(null)
let refreshTimer: number | undefined

const overview = computed(() => snapshot.value?.overview)
const lastUpdated = computed(() => snapshot.value ? new Date(snapshot.value.generated_at).toLocaleString() : '—')
const visibleGroups = computed(() => groups.value.filter((group) => !platform.value || group.platform === platform.value))
const rankSections = computed(() => [
  { title: '模型分布', description: '按调用次数排序', items: snapshot.value?.top_models ?? [], tone: 'amber' },
  { title: '分组负载', description: '观察路由池是否失衡', items: snapshot.value?.top_groups ?? [], tone: 'cyan' },
  { title: '调用用户', description: '最多展示前 8 位', items: snapshot.value?.top_users ?? [], tone: 'green' },
])

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const query = new URLSearchParams({ range: range.value })
    if (platform.value) query.set('platform', platform.value)
    if (groupID.value) query.set('group_id', groupID.value)
    snapshot.value = normalizeSnapshot(await api.get<OpsSnapshot>(`/api/admin/ops/snapshot?${query}`))
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '暂时无法读取监控数据'
  } finally {
    loading.value = false
  }
}

function resetGroupWhenPlatformChanges() {
  const selected = groups.value.find((group) => String(group.id) === groupID.value)
  if (selected && platform.value && selected.platform !== platform.value) groupID.value = ''
}

function setRange(value: string) {
  range.value = value
}

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
    : { captured_at: '', in_flight: 0, last_minute: emptyWindow, breakdown: [] }
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
    realtime: { ...realtime, last_minute: realtime.last_minute || emptyWindow, breakdown: Array.isArray(realtime.breakdown) ? realtime.breakdown : [] },
  }
}

function setupTimer() {
  if (refreshTimer) window.clearInterval(refreshTimer)
  refreshTimer = window.setInterval(() => {
    if (autoRefresh.value && !loading.value) void load()
  }, 30_000)
}

watch([range, platform, groupID], () => void load())
watch(platform, resetGroupWhenPlatformChanges)

onMounted(async () => {
  groups.value = await api.get<Group[]>('/api/admin/groups')
  await load()
  setupTimer()
})
onBeforeUnmount(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
})
</script>

<template>
  <div class="ops-page">
    <header class="console-page-head ops-page-head">
      <div>
        <div class="ops-eyebrow"><span></span> 运行监控</div>
        <h1>服务运行情况</h1>
        <p>已完成请求取自用量账本；并发、每分钟吞吐和上游探测实时分层呈现。</p>
      </div>
      <div class="ops-page-actions">
        <label class="ops-auto-refresh">
          <input v-model="autoRefresh" type="checkbox" />
          <span>30 秒刷新</span>
        </label>
        <button class="btn-ghost" :disabled="probingAll" @click="triggerAllProbes">{{ probingAll ? '检测中…' : '检查全部账户' }}</button>
        <button class="btn-ghost" :disabled="loading" @click="load">{{ loading ? '更新中' : '刷新' }}</button>
      </div>
    </header>

    <section class="ops-filterbar" aria-label="监控筛选">
      <div class="ops-range-tabs" role="tablist" aria-label="时间范围">
        <button v-for="item in [{ value: '1h', label: '1 小时' }, { value: '24h', label: '24 小时' }, { value: '7d', label: '7 天' }, { value: '30d', label: '30 天' }]" :key="item.value" type="button" :class="{ 'is-active': range === item.value }" @click="setRange(item.value)">{{ item.label }}</button>
      </div>
      <div class="ops-filter-selects">
        <select v-model="platform" class="input">
          <option value="">全部平台</option>
          <option value="anthropic">Claude</option>
          <option value="openai">OpenAI</option>
          <option value="gemini">Gemini</option>
        </select>
        <select v-model="groupID" class="input">
          <option value="">全部分组</option>
          <option v-for="group in visibleGroups" :key="group.id" :value="String(group.id)">{{ group.name }}</option>
        </select>
      </div>
      <span class="ops-updated">更新于 {{ lastUpdated }}</span>
    </section>

    <div v-if="loadError" class="ops-error-state">
      <span>{{ loadError }}</span>
      <button class="btn-ghost !px-3 !py-1 text-xs" @click="load">重试</button>
    </div>

    <template v-else-if="overview">
      <section class="ops-pulse" aria-label="运行概况">
        <div class="ops-health-score">
          <div class="ops-score-ring" :style="{ '--score': `${overview.health_score}%` }"><strong>{{ overview.health_score }}</strong><span>/100</span></div>
          <div>
            <div class="ops-metric-label">运行健康度</div>
            <p>{{ overview.account_available }} 个账号可用，{{ overview.account_cooling }} 个处于冷却。</p>
          </div>
        </div>
        <dl class="ops-pulse-metrics">
          <div><dt>请求</dt><dd>{{ overview.requests.toLocaleString() }}</dd><small>最近 5 分钟 {{ overview.last_5_minutes.requests }} 次</small></div>
          <div><dt>成功率</dt><dd :class="overview.success_rate >= 99 ? 'text-signal-green' : 'text-amber'">{{ percent(overview.success_rate) }}</dd><small>失败 {{ overview.error_requests.toLocaleString() }} 次</small></div>
          <div><dt>P95 延迟</dt><dd>{{ formatLatency(overview.p95_latency_ms) }}</dd><small>P50 {{ formatLatency(overview.p50_latency_ms) }}</small></div>
          <div><dt>Token</dt><dd>{{ formatTokens(overview.total_tokens) }}</dd><small>输入 {{ formatTokens(overview.input_tokens) }} · 输出 {{ formatTokens(overview.output_tokens) }}</small></div>
          <div><dt>账面用量</dt><dd>{{ formatMoney(overview.cost_micro) }}</dd><small>近 5 分钟 {{ formatMoney(overview.last_5_minutes.cost_micro) }}</small></div>
        </dl>
      </section>

      <section class="ops-realtime-grid" aria-label="实时流量">
        <article class="card ops-realtime-card">
          <div class="ops-section-title"><div><h3>实时流量</h3><p>已完成请求按最近 1 分钟统计；进行中的流式请求单独计数。</p></div><span class="ops-live-indicator"><i></i> 实时</span></div>
          <dl class="ops-realtime-metrics">
            <div><dt>进行中</dt><dd>{{ snapshot?.realtime.in_flight ?? 0 }}</dd><small>正在转发的请求</small></div>
            <div><dt>最近 1 分钟</dt><dd>{{ snapshot?.realtime.last_minute.requests ?? 0 }}</dd><small>已完成请求</small></div>
            <div><dt>QPS</dt><dd>{{ (snapshot?.realtime.last_minute.requests_per_second ?? 0).toFixed(2) }}</dd><small>每秒请求</small></div>
            <div><dt>TPS</dt><dd>{{ (snapshot?.realtime.last_minute.tokens_per_second ?? 0).toFixed(1) }}</dd><small>每秒 Token</small></div>
          </dl>
          <div v-if="snapshot?.realtime.breakdown.length" class="ops-live-breakdown">
            <span v-for="item in snapshot?.realtime.breakdown" :key="`${item.scope}-${item.id || item.name}`"><b>{{ item.in_flight }}</b> {{ item.name }}</span>
          </div>
        </article>
        <article class="card ops-realtime-card ops-token-detail">
          <div class="ops-section-title"><div><h3>当前筛选 Token</h3><p>完成后写入账本，缓存写入按实际 TTL 分列。</p></div></div>
          <dl class="ops-token-mini">
            <div><dt>输入</dt><dd>{{ formatTokens(snapshot?.model_usage.reduce((sum, item) => sum + item.input_tokens, 0) ?? 0) }}</dd></div>
            <div><dt>输出</dt><dd>{{ formatTokens(snapshot?.model_usage.reduce((sum, item) => sum + item.output_tokens, 0) ?? 0) }}</dd></div>
            <div><dt>缓存读</dt><dd>{{ formatTokens(snapshot?.model_usage.reduce((sum, item) => sum + item.cache_read_tokens, 0) ?? 0) }}</dd></div>
          </dl>
          <p class="ops-token-note">上方为当前筛选周期的模型累计；下方明细可核对每个模型的输入、输出和缓存。</p>
        </article>
      </section>

      <section class="ops-main-grid">
        <OpsTrendChart :items="snapshot?.trend ?? []" />
        <aside class="ops-account-brief card">
          <div class="ops-section-title">
            <div><h3>账号池</h3><p>上游可用性</p></div>
            <RouterLink to="/admin/accounts" class="ops-link">管理账号</RouterLink>
          </div>
          <dl class="ops-health-list">
            <div><dt>已验证</dt><dd class="text-signal-green">{{ overview.account_available }}</dd></div>
            <div><dt>待关注</dt><dd class="text-amber">{{ overview.account_attention }}</dd></div>
            <div><dt>冷却中</dt><dd class="text-signal-cyan">{{ overview.account_cooling }}</dd></div>
            <div><dt>已停用</dt><dd class="text-slate-500">{{ overview.account_disabled }}</dd></div>
          </dl>
          <div class="ops-current-rate"><span>近 5 分钟</span><strong>{{ overview.last_5_minutes.requests_per_minute.toFixed(1) }} <em>次/分钟</em></strong></div>
          <div v-if="snapshot?.system" class="ops-system-mini">
            <div><span>进程内存</span><b>{{ formatBytes(snapshot.system.memory_alloc_bytes) }}</b></div>
            <div><span>Goroutine</span><b>{{ snapshot.system.goroutines }}</b></div>
            <div><span>数据库连接</span><b>{{ snapshot.system.db_in_use }} / {{ snapshot.system.db_open_connections }}</b></div>
          </div>
        </aside>
      </section>

      <p v-if="snapshot?.sample_truncated" class="ops-sample-note">明细样本已超过 50,000 条；总请求、费用和成功率仍为完整统计，趋势与排行按最近样本计算。</p>

      <section class="ops-rank-grid">
        <article v-for="section in rankSections" :key="section.title" class="ops-rank-panel card">
          <div class="ops-section-title"><div><h3>{{ section.title }}</h3><p>{{ section.description }}</p></div></div>
          <div v-if="section.items.length" class="ops-rank-list">
            <div v-for="(rank, index) in section.items" :key="`${rank.id || rank.name}-${index}`" class="ops-rank-row">
              <span class="ops-rank-index">{{ String(index + 1).padStart(2, '0') }}</span>
              <div class="min-w-0"><strong :title="rank.name">{{ rank.name }}</strong><small>{{ rank.requests.toLocaleString() }} 次 · {{ formatTokens(rank.tokens) }} Token</small></div>
              <div class="text-right"><b>{{ percent(rankErrorRate(rank)) }}</b><small>失败率</small></div>
            </div>
          </div>
          <div v-else class="ops-empty">暂无调用</div>
        </article>
      </section>

      <section class="ops-detail-stack">
        <article class="card overflow-hidden">
          <div class="ops-section-title ops-table-title"><div><h3>模型用量明细</h3><p>按账面费用排序，含输入、输出、缓存读写、请求数和失败率。</p></div></div>
          <div class="overflow-x-auto">
            <table class="table-base ops-model-table">
              <thead><tr><th>模型</th><th class="text-right">请求</th><th class="text-right">输入</th><th class="text-right">输出</th><th class="text-right">缓存读</th><th class="text-right">5m 写入</th><th class="text-right">1h 写入</th><th class="text-right">失败率</th><th class="text-right">P 均耗时</th><th class="text-right">费用</th></tr></thead>
              <tbody>
                <tr v-for="item in snapshot?.model_usage" :key="item.name">
                  <td class="font-mono text-xs text-slate-200">{{ item.name }}</td>
                  <td class="num text-right">{{ item.requests.toLocaleString() }}</td>
                  <td class="num text-right">{{ formatTokens(item.input_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.output_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.cache_read_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.cache_write_5m_tokens) }}</td>
                  <td class="num text-right">{{ formatTokens(item.cache_write_1h_tokens) }}</td>
                  <td class="num text-right" :class="rankErrorRate(item) ? 'text-amber' : 'text-signal-green'">{{ percent(rankErrorRate(item)) }}</td>
                  <td class="num text-right">{{ formatLatency(item.average_latency_ms) }}</td>
                  <td class="num text-right text-slate-200">{{ formatMoney(item.cost_micro) }}</td>
                </tr>
                <tr v-if="!snapshot?.model_usage.length"><td colspan="10" class="py-10 text-center text-sm text-slate-500">当前时间段还没有模型调用</td></tr>
              </tbody>
            </table>
          </div>
        </article>

        <article class="card overflow-hidden">
          <div class="ops-section-title ops-table-title"><div><h3>当前倍率配置</h3><p>这些是此刻的分组设置；历史费用以调用完成时写入账本的金额为准，不会被后续改价回溯。</p></div><RouterLink to="/admin/groups" class="ops-link">管理倍率</RouterLink></div>
          <div class="overflow-x-auto">
            <table class="table-base ops-rate-table">
              <thead><tr><th>分组</th><th>平台</th><th class="text-right">文本</th><th class="text-right">缓存读</th><th class="text-right">5m 缓存写</th><th class="text-right">1h 缓存写</th><th class="text-right">生图</th></tr></thead>
              <tbody>
                <tr v-for="profile in snapshot?.rate_profiles" :key="profile.id">
                  <td class="font-medium text-slate-200">{{ profile.name }}</td>
                  <td><span class="tag-gray">{{ PLATFORM_LABELS[profile.platform] || profile.platform }}</span></td>
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
          <div class="ops-section-title ops-table-title"><div><h3>账号状态</h3><p>状态来自最近的主动探测；OAuth 不生成内容，只检查令牌期限和传输链路。</p></div></div>
          <div class="overflow-x-auto">
            <table class="table-base ops-account-table">
              <thead><tr><th>账号</th><th>分组</th><th>状态</th><th>最近探测</th><th>探测结果</th><th class="text-right">错误次数</th><th>操作</th></tr></thead>
              <tbody>
                <tr v-for="account in snapshot?.account_health" :key="account.id">
                  <td><div class="font-medium text-slate-200">{{ account.name }}</div><div class="text-xs text-slate-500">{{ account.email || PLATFORM_LABELS[account.platform] || account.platform }}</div></td>
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
          <div class="ops-section-title ops-table-title"><div><h3>最近失败</h3><p>显示最近 12 条非成功调用，可前往用量明细查看完整账本。</p></div><RouterLink to="/admin/usage?status=error" class="ops-link">查看明细</RouterLink></div>
          <div class="overflow-x-auto">
            <table class="table-base ops-error-table">
              <thead><tr><th>时间</th><th>用户 / 密钥</th><th>模型</th><th>上游账号</th><th>错误</th><th class="text-right">耗时</th></tr></thead>
              <tbody>
                <tr v-for="item in snapshot?.recent_errors" :key="item.id">
                  <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(item.created_at).toLocaleString() }}</td>
                  <td><div class="text-xs text-slate-300">{{ item.user_email || '—' }}</div><div class="text-[11px] text-slate-500">{{ item.key_name || '未命名密钥' }}</div></td>
                  <td class="font-mono text-xs text-slate-200">{{ item.model || '—' }}</td>
                  <td class="text-xs text-slate-400">{{ item.account_name || '—' }}</td>
					<td class="max-w-sm truncate text-xs text-signal-red" :title="item.error_message"><span class="mr-1 font-mono">{{ item.status_code }}</span>{{ summarizeProviderError(item.error_message || '上游返回失败') }}</td>
                  <td class="num text-right text-xs text-slate-500">{{ formatLatency(item.duration_ms) }}</td>
                </tr>
                <tr v-if="!snapshot?.recent_errors.length"><td colspan="6" class="py-10 text-center text-sm text-slate-500">当前时间段没有失败记录</td></tr>
              </tbody>
            </table>
          </div>
        </article>
      </section>
    </template>

    <div v-else class="ops-loading-state">正在读取运行数据…</div>
  </div>
</template>
