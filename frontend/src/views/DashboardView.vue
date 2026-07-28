<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, copyText } from '../api/client'
import type {
  ApiKey,
  ChannelGroupStatus,
  ChannelStatusResponse,
  Group,
  UsageLog,
  UsageSummary,
} from '../api/types'
import { formatMoney, formatTokens, PLATFORM_LABELS } from '../api/types'
import { useAuth } from '../stores/auth'
import { useToast } from '../stores/toast'
import UsageChart from '../components/UsageChart.vue'

const auth = useAuth()
const toast = useToast()
const summary = ref<UsageSummary | null>(null)
const keys = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
const usageItems = ref<UsageLog[]>([])
const usageTotal = ref(0)
const channelGroups = ref<ChannelGroupStatus[]>([])
const loading = ref(true)
const loadError = ref('')
const endpoint = `${window.location.origin}/v1`

const activeKeys = computed(() => keys.value.filter((key) => key.status === 'active'))
const activeGroups = computed(() => groups.value.filter((group) => group.status === 'active'))
const monthTokens = computed(() => (summary.value?.month.input_tokens ?? 0) + (summary.value?.month.output_tokens ?? 0))
const todayTokens = computed(() => (summary.value?.today.input_tokens ?? 0) + (summary.value?.today.output_tokens ?? 0))
const averageRequestCost = computed(() => {
  const requests = summary.value?.month.requests ?? 0
  return requests > 0 ? Math.round((summary.value?.month.cost_micro ?? 0) / requests) : 0
})
const inputShare = computed(() => {
  const total = monthTokens.value
  return total > 0 ? Math.round(((summary.value?.month.input_tokens ?? 0) / total) * 100) : 0
})
const outputShare = computed(() => monthTokens.value > 0 ? 100 - inputShare.value : 0)
const requestTrend = computed(() => {
  const rows = summary.value?.daily ?? []
  const current = rows.at(-1)?.requests ?? 0
  const previous = rows.at(-2)?.requests ?? 0
  if (!previous) return current ? '今日已有调用' : '今日暂无调用'
  const percent = Math.round(((current - previous) / previous) * 100)
  return `${percent >= 0 ? '+' : ''}${percent}% 较昨日`
})
const lastUsedAt = computed(() => {
  const timestamps = keys.value
    .map((key) => key.last_used_at ? new Date(key.last_used_at).getTime() : 0)
    .filter(Boolean)
  return timestamps.length ? Math.max(...timestamps) : 0
})
const platformSummary = computed(() => {
  const platforms = [...new Set(activeGroups.value.map((group) => group.platform))]
  return platforms.length ? platforms.map((platform) => PLATFORM_LABELS[platform] || platform).join(' · ') : '暂无可用分组'
})
const recentSuccessRate = computed(() => {
  if (!usageItems.value.length) return 0
  const successes = usageItems.value.filter((item) => item.status_code >= 200 && item.status_code < 400).length
  return (successes / usageItems.value.length) * 100
})
const recentCacheRate = computed(() => {
  const cache = usageItems.value.reduce((total, item) => total + item.cache_read_tokens, 0)
  const input = usageItems.value.reduce((total, item) => total + item.input_tokens, 0)
  const denominator = input + cache
  return denominator > 0 ? (cache / denominator) * 100 : 0
})
const recentFirstToken = computed(() => {
  const samples = usageItems.value.map((item) => item.first_token_ms).filter((value) => value > 0)
  return samples.length ? Math.round(samples.reduce((total, value) => total + value, 0) / samples.length) : 0
})
const modelRanking = computed(() => {
  const buckets = new Map<string, { name: string; requests: number; tokens: number; costMicro: number }>()
  for (const item of usageItems.value) {
    const name = item.model || '未记录模型'
    const current = buckets.get(name) || { name, requests: 0, tokens: 0, costMicro: 0 }
    current.requests += 1
    current.tokens += item.input_tokens + item.output_tokens + item.cache_read_tokens + item.cache_write_tokens
    current.costMicro += item.cost_micro
    buckets.set(name, current)
  }
  const rows = [...buckets.values()].sort((a, b) => b.requests - a.requests || b.tokens - a.tokens).slice(0, 4)
  const total = rows.reduce((sum, row) => sum + row.requests, 0)
  return rows.map((row, index) => ({
    ...row,
    share: total > 0 ? Math.round((row.requests / total) * 100) : 0,
    tone: `is-tone-${index + 1}`,
  }))
})
const visibleChannels = computed(() => [...channelGroups.value]
  .sort((a, b) => channelStateOrder(a.state) - channelStateOrder(b.state) || b.request_total - a.request_total)
  .slice(0, 6))
const healthyChannelCount = computed(() => channelGroups.value.filter((group) => group.state === 'healthy').length)
const dateLabel = new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric', weekday: 'short' }).format(new Date())

function channelStateOrder(state: ChannelGroupStatus['state']) {
  if (state === 'down' || state === 'expired') return 0
  if (state === 'degraded' || state === 'unknown') return 1
  if (state === 'disabled') return 2
  return 3
}

function channelStateLabel(state: ChannelGroupStatus['state']) {
  if (state === 'healthy') return '正常'
  if (state === 'degraded') return '波动'
  if (state === 'down') return '异常'
  if (state === 'expired') return '已过期'
  if (state === 'disabled') return '已停用'
  return '待探测'
}

function relativeTime(timestamp: number) {
  if (!timestamp) return '尚未调用'
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (seconds < 60) return '刚刚'
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return `${Math.floor(seconds / 86400)} 天前`
}

function requestTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? '—'
    : new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(date)
}

function requestLatency(item: UsageLog) {
  const value = item.first_token_ms || item.duration_ms
  if (!value) return '—'
  return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${value}ms`
}

function requestEndpoint(item: UsageLog) {
  return item.request_path || (item.platform === 'anthropic' ? '/v1/messages' : '/v1/responses')
}

function requestLink(item: UsageLog) {
  return item.request_id ? `/usage?request_id=${encodeURIComponent(item.request_id)}` : '/usage'
}

async function loadDashboard() {
  loading.value = true
  loadError.value = ''
  try {
    const [usage, keyList, groupList, recent, channels] = await Promise.all([
      api.get<UsageSummary>('/api/user/usage/summary'),
      api.get<ApiKey[]>('/api/user/keys'),
      api.get<Group[]>('/api/user/groups'),
      api.get<{ items: UsageLog[]; total: number }>('/api/user/usage?page=1&size=12&sort=created_at&order=desc')
        .catch(() => ({ items: [], total: 0 })),
      api.get<ChannelStatusResponse>('/api/user/channel-status?range=24h')
        .catch(() => ({ groups: [] } as unknown as ChannelStatusResponse)),
      auth.fetchMe(),
    ])
    summary.value = usage
    keys.value = keyList
    groups.value = groupList
    usageItems.value = recent.items || []
    usageTotal.value = recent.total || 0
    channelGroups.value = channels.groups || []
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '总览数据加载失败'
  } finally {
    loading.value = false
  }
}

async function copyEndpoint() {
  try {
    await copyText(endpoint)
    toast.show('接口地址已复制', 'success')
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '复制失败', 'error')
  }
}

onMounted(loadDashboard)
</script>

<template>
  <div class="dashboard-page dashboard-editorial">
    <header class="dashboard-head dashboard-editorial-head">
      <div>
        <div class="dashboard-editorial-status"><span></span>{{ dateLabel }} · 服务正常</div>
        <h1>运营总览</h1>
      </div>
      <div class="dashboard-head-actions">
        <RouterLink class="btn-ghost" to="/usage">用量明细</RouterLink>
        <RouterLink class="btn-primary" to="/keys">新建密钥</RouterLink>
      </div>
    </header>

    <div v-if="loading" class="dashboard-skeleton" aria-label="正在加载总览">
      <span class="dashboard-skeleton-wide"></span>
      <span></span><span></span><span></span>
    </div>

    <div v-else-if="loadError" class="dashboard-error" role="alert">
      <div><strong>总览暂时没有加载完成</strong><p>{{ loadError }}</p></div>
      <button class="btn-ghost" @click="loadDashboard">重新加载</button>
    </div>

    <template v-else>
      <section class="dashboard-editorial-metrics" aria-label="今日运行摘要">
        <div>
          <span>今日请求</span>
          <strong>{{ (summary?.today.requests ?? 0).toLocaleString() }}</strong>
          <small>{{ requestTrend }}</small>
        </div>
        <div>
          <span>今日 Token</span>
          <strong>{{ formatTokens(todayTokens) }}</strong>
          <small>输入与输出合计</small>
        </div>
        <div>
          <span>最近成功率</span>
          <strong>{{ recentSuccessRate.toFixed(2) }}%</strong>
          <small>{{ usageItems.length }} 条最近调用</small>
        </div>
        <div>
          <span>首字延迟</span>
          <strong>{{ recentFirstToken ? `${recentFirstToken} ms` : '—' }}</strong>
          <small>最近有效样本均值</small>
        </div>
      </section>

      <div class="dashboard-editorial-board">
        <UsageChart class="dashboard-editorial-traffic" :daily="summary?.daily ?? []" />

        <article class="dashboard-editorial-spend">
          <span>30 天费用</span>
          <strong>{{ formatMoney(summary?.month.cost_micro ?? 0) }}</strong>
          <small>{{ (summary?.month.requests ?? 0).toLocaleString() }} 次请求</small>
          <RouterLink to="/wallet">查看账单 <b>→</b></RouterLink>
        </article>

        <article class="dashboard-editorial-cache">
          <header><span>缓存命中</span><i aria-hidden="true"></i></header>
          <strong>{{ recentCacheRate.toFixed(1) }}%</strong>
          <small>最近请求 Token</small>
          <div class="dashboard-editorial-dot-matrix" aria-hidden="true">
            <i v-for="index in 30" :key="index" :class="{ 'is-active': index <= Math.round(recentCacheRate * .3) }"></i>
          </div>
        </article>

        <section class="dashboard-editorial-live">
          <header>
            <div><span class="dashboard-editorial-live-dot"></span><h2>实时请求</h2></div>
            <RouterLink to="/usage">共 {{ usageTotal.toLocaleString() }} 条</RouterLink>
          </header>
          <div v-if="usageItems.length" class="dashboard-editorial-live-list">
            <RouterLink v-for="item in usageItems.slice(0, 7)" :key="item.id" :to="requestLink(item)">
              <span class="dashboard-editorial-method" :class="{ 'is-error': item.status_code >= 400 }">POST</span>
              <span class="dashboard-editorial-request-main">
                <strong>{{ requestEndpoint(item) }}</strong>
                <small>{{ item.model || '未记录模型' }}</small>
              </span>
              <span class="dashboard-editorial-request-meta">
                <b>{{ requestLatency(item) }}</b>
                <small>{{ requestTime(item.created_at) }}</small>
              </span>
              <i :class="{ 'is-error': item.status_code >= 400 }"></i>
            </RouterLink>
          </div>
          <div v-else class="dashboard-editorial-empty">还没有调用记录</div>
        </section>

        <section class="dashboard-editorial-models">
          <header><h2>模型用量</h2><span>最近 {{ usageItems.length }} 次调用</span></header>
          <div v-if="modelRanking.length" class="dashboard-editorial-model-list">
            <div v-for="model in modelRanking" :key="model.name" :class="model.tone">
              <header><strong :title="model.name">{{ model.name }}</strong><b>{{ model.share }}%</b></header>
              <div><i :style="{ width: `${Math.max(model.share, 4)}%` }"></i></div>
              <footer><span>{{ model.requests }} 次</span><span>{{ formatTokens(model.tokens) }}</span><span>{{ formatMoney(model.costMicro) }}</span></footer>
            </div>
          </div>
          <div v-else class="dashboard-editorial-empty">暂无模型用量</div>
        </section>

        <section class="dashboard-editorial-channels">
          <header>
            <div><h2>渠道健康</h2><span>{{ healthyChannelCount }} / {{ channelGroups.length }} 正常</span></div>
            <RouterLink to="/status">查看全部 →</RouterLink>
          </header>
          <div v-if="visibleChannels.length" class="dashboard-editorial-channel-list">
            <div v-for="channel in visibleChannels" :key="channel.id">
              <span class="dashboard-editorial-channel-mark" :class="`is-${channel.state}`"></span>
              <span class="dashboard-editorial-channel-name">
                <strong :title="channel.name">{{ channel.name }}</strong>
                <small>{{ PLATFORM_LABELS[channel.platform] || channel.platform }} · {{ channel.top_model || '自动路由' }}</small>
              </span>
              <span><small>成功率</small><b>{{ channel.request_success_rate.toFixed(1) }}%</b></span>
              <span><small>首字</small><b>{{ channel.average_ttft_ms ? `${Math.round(channel.average_ttft_ms)} ms` : '—' }}</b></span>
              <em :class="`is-${channel.state}`">{{ channelStateLabel(channel.state) }}</em>
            </div>
          </div>
          <div v-else class="dashboard-editorial-empty">暂无渠道探测数据</div>
        </section>

        <section class="dashboard-editorial-access">
          <div class="dashboard-editorial-balance">
            <span>账户余额</span>
            <strong>{{ formatMoney(auth.user?.balance_micro ?? 0) }}</strong>
            <RouterLink to="/wallet">充值</RouterLink>
          </div>
          <dl>
            <div><dt>活跃密钥</dt><dd>{{ activeKeys.length }} / {{ keys.length }}</dd></div>
            <div><dt>可用分组</dt><dd>{{ activeGroups.length }} / {{ groups.length }}</dd></div>
            <div><dt>最近调用</dt><dd>{{ relativeTime(lastUsedAt) }}</dd></div>
            <div><dt>单次均价</dt><dd>{{ formatMoney(averageRequestCost) }}</dd></div>
          </dl>
          <button class="dashboard-editorial-endpoint" type="button" @click="copyEndpoint">
            <span><small>BASE URL · {{ platformSummary }}</small><code>{{ endpoint }}</code></span>
            <b>复制</b>
          </button>
        </section>
      </div>
    </template>
  </div>
</template>
