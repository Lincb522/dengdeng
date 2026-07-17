<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, copyText } from '../api/client'
import type { ApiKey, Group, UsageSummary } from '../api/types'
import { formatMoney, formatTokens, PLATFORM_LABELS } from '../api/types'
import { useAuth } from '../stores/auth'
import { useToast } from '../stores/toast'
import UsageChart from '../components/UsageChart.vue'

const auth = useAuth()
const toast = useToast()
const summary = ref<UsageSummary | null>(null)
const keys = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
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
const dateLabel = new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric', weekday: 'short' }).format(new Date())

function relativeTime(timestamp: number) {
  if (!timestamp) return '尚未调用'
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (seconds < 60) return '刚刚'
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return `${Math.floor(seconds / 86400)} 天前`
}

async function loadDashboard() {
  loading.value = true
  loadError.value = ''
  try {
    const [usage, keyList, groupList] = await Promise.all([
      api.get<UsageSummary>('/api/user/usage/summary'),
      api.get<ApiKey[]>('/api/user/keys'),
      api.get<Group[]>('/api/user/groups'),
      auth.fetchMe(),
    ])
    summary.value = usage
    keys.value = keyList
    groups.value = groupList
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
  <div class="dashboard-page">
    <header class="dashboard-head">
      <div>
        <div class="dashboard-date"><span></span>{{ dateLabel }} · 服务正常</div>
        <h1>总览</h1>
        <p>账户资金、调用趋势与接入状态。</p>
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
      <section class="dashboard-ledger" aria-label="账户与调用摘要">
        <div class="dashboard-ledger-balance">
          <div class="dashboard-ledger-label">可用余额</div>
          <div class="dashboard-ledger-value">{{ formatMoney(auth.user?.balance_micro ?? 0) }}</div>
          <RouterLink to="/wallet">充值与兑换 <span>→</span></RouterLink>
        </div>
        <dl class="dashboard-ledger-stats">
          <div><dt>今日请求</dt><dd>{{ (summary?.today.requests ?? 0).toLocaleString() }}</dd><small>{{ requestTrend }}</small></div>
          <div><dt>今日 Token</dt><dd>{{ formatTokens(todayTokens) }}</dd><small>输入与输出合计</small></div>
          <div><dt>30 天消费</dt><dd>{{ formatMoney(summary?.month.cost_micro ?? 0) }}</dd><small>{{ (summary?.month.requests ?? 0).toLocaleString() }} 次请求</small></div>
        </dl>
      </section>

      <section class="dashboard-main-grid">
        <UsageChart class="dashboard-usage" :daily="summary?.daily ?? []" />

        <aside class="dashboard-panel dashboard-access">
          <div class="dashboard-panel-head"><div><h2>接入状态</h2><p>当前账户的可用资源</p></div><span class="dashboard-live-dot" title="服务正常"></span></div>
          <div class="dashboard-access-list">
            <div><span>活跃密钥</span><strong>{{ activeKeys.length }} <small>/ {{ keys.length }}</small></strong></div>
            <div><span>可用分组</span><strong>{{ activeGroups.length }} <small>/ {{ groups.length }}</small></strong></div>
            <div><span>最近调用</span><strong>{{ relativeTime(lastUsedAt) }}</strong></div>
          </div>
          <p class="dashboard-platforms">{{ platformSummary }}</p>
          <button class="dashboard-endpoint" type="button" @click="copyEndpoint">
            <span><small>BASE URL</small><code>{{ endpoint }}</code></span>
            <b>复制</b>
          </button>
        </aside>
      </section>

      <section class="dashboard-lower-grid">
        <article class="dashboard-panel dashboard-token-mix">
          <div class="dashboard-panel-head"><div><h2>30 天 Token 结构</h2><p>计费前的输入与输出用量</p></div><strong>{{ formatTokens(monthTokens) }}</strong></div>
          <div class="dashboard-token-bar" aria-hidden="true"><i :style="{ width: `${inputShare}%` }"></i><b :style="{ width: `${outputShare}%` }"></b></div>
          <div class="dashboard-token-legend">
            <div><span class="is-input"></span><p>输入 Token<small>{{ formatTokens(summary?.month.input_tokens ?? 0) }} · {{ inputShare }}%</small></p></div>
            <div><span class="is-output"></span><p>输出 Token<small>{{ formatTokens(summary?.month.output_tokens ?? 0) }} · {{ outputShare }}%</small></p></div>
            <div><span class="is-cost"></span><p>单次均价<small>{{ formatMoney(averageRequestCost) }}</small></p></div>
          </div>
        </article>

        <nav class="dashboard-panel dashboard-shortcuts" aria-label="常用入口">
          <div class="dashboard-panel-head"><div><h2>常用入口</h2><p>继续完成接入与管理</p></div></div>
          <RouterLink to="/keys"><span>API 密钥<small>创建、限额与快速配置</small></span><b>→</b></RouterLink>
          <RouterLink to="/models"><span>模型广场<small>查看模型能力和可用分组</small></span><b>→</b></RouterLink>
          <RouterLink to="/wallet"><span>钱包<small>充值、兑换与账单记录</small></span><b>→</b></RouterLink>
        </nav>
      </section>
    </template>
  </div>
</template>
