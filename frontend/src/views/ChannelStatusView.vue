<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { ChannelGroupStatus, ChannelStatusResponse } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'
import ProviderLogo from '../components/ProviderLogo.vue'

const ranges = [
  { value: '1h', label: '1 小时' },
  { value: '3h', label: '3 小时' },
  { value: '24h', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '15d', label: '15 天' },
]
const platformOrder = ['openai', 'anthropic', 'gemini', 'grok', 'kimi', 'zhipu', 'deepseek', 'composite']
const selectedRange = ref('1h')
const data = ref<ChannelStatusResponse | null>(null)
const loading = ref(true)
const refreshing = ref(false)
const error = ref('')
let refreshTimer: ReturnType<typeof window.setInterval> | null = null

const groups = computed(() => Array.isArray(data.value?.groups) ? data.value.groups : [])
const groupedChannels = computed(() => {
  const result = new Map<string, ChannelGroupStatus[]>()
  for (const group of groups.value) {
    if (!result.has(group.platform)) result.set(group.platform, [])
    result.get(group.platform)!.push(group)
  }
  return [...result.entries()].sort(([left], [right]) => {
    const leftIndex = platformOrder.indexOf(left)
    const rightIndex = platformOrder.indexOf(right)
    return (leftIndex < 0 ? 99 : leftIndex) - (rightIndex < 0 ? 99 : rightIndex) || left.localeCompare(right)
  })
})
const stateCounts = computed(() => groups.value.reduce((counts, group) => {
  const key = group.state === 'expired' ? 'down' : group.state
  counts[key] = (counts[key] || 0) + 1
  return counts
}, {} as Record<string, number>))
const overallState = computed(() => {
  const active = groups.value.filter((group) => group.state !== 'disabled')
  if (!active.length) return groups.value.length ? 'disabled' : 'unknown'
  if (active.every((group) => group.state === 'down' || group.state === 'expired')) return 'down'
  if (active.some((group) => ['down', 'expired', 'degraded'].includes(group.state))) return 'degraded'
  if (active.every((group) => group.state === 'unknown')) return 'unknown'
  if (active.some((group) => group.state === 'unknown')) return 'degraded'
  return 'healthy'
})
const selectedRangeLabel = computed(() => ranges.find((item) => item.value === selectedRange.value)?.label || selectedRange.value)

function platformLabel(value: string) {
  return PLATFORM_LABELS[value] || value
}

function platformFamily(value: string) {
  return ({ openai: 'GPT 系列', anthropic: 'Claude 系列', gemini: 'Gemini 系列', grok: 'Grok 系列' } as Record<string, string>)[value] || `${platformLabel(value)} 系列`
}

function stateLabel(value: string) {
  return ({ healthy: '正常', degraded: '波动', down: '故障', expired: '凭证过期', disabled: '已停用', unknown: '待探测' } as Record<string, string>)[value] || value
}

function overallStateLabel(value: string) {
  return ({ healthy: '全部正常', degraded: '部分异常', down: '全部故障', disabled: '全部停用', unknown: '等待探测' } as Record<string, string>)[value] || stateLabel(value)
}

function stateSourceLabel(value: string) {
  return ({ traffic: '实际请求', probe: '自动巡检', accounts: '账号池', group: '分组设置' } as Record<string, string>)[value] || '自动巡检'
}

function formatDate(value?: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '暂无数据'
}

function formatRelative(value?: string | null) {
  if (!value) return '暂无记录'
  const elapsed = Math.max(0, Date.now() - new Date(value).getTime())
  if (elapsed < 60_000) return '刚刚'
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`
  return new Date(value).toLocaleDateString('zh-CN')
}

function formatInterval(seconds?: number) {
  if (!seconds) return '自动巡检'
  if (seconds % 3600 === 0) return `每 ${seconds / 3600} 小时巡检`
  if (seconds % 60 === 0) return `每 ${seconds / 60} 分钟巡检`
  return `每 ${seconds} 秒巡检`
}

function formatLatency(value: number) {
  return value > 0 ? `${Math.round(value)} ms` : '—'
}

function formatRate(value: number, total: number) {
  return total > 0 ? `${value.toFixed(2)}%` : '—'
}

function rateClass(value: number, total: number) {
  if (!total) return 'is-unknown'
  if (value >= 98) return 'is-good'
  if (value >= 80) return 'is-warning'
  return 'is-bad'
}

function currentEvidence(group: ChannelGroupStatus) {
  if (group.state_source === 'traffic') {
    return `${group.current_request_successes}/${group.current_request_total} 次成功 · 近 ${data.value?.current_window_minutes || 15} 分钟`
  }
  if (group.state_source === 'accounts') return '当前没有可调度账号'
  if (group.state_source === 'group') return '分组已停用'
  return formatRelative(group.last_probe_at)
}

function accountValue(group: ChannelGroupStatus) {
  if (!group.account_eligible) return `0 / ${group.account_total}`
  if (!group.account_checked) return `— / ${group.account_eligible}`
  return `${group.account_available} / ${group.account_eligible}`
}

function accountDetail(group: ChannelGroupStatus) {
  if (!group.account_eligible) return `${group.account_total} 个账号，当前均不可调度`
  if (!group.account_checked) return `${group.account_eligible} 个可调度账号等待巡检`
  if (group.account_checked < group.account_eligible) return `已巡检 ${group.account_checked} / ${group.account_eligible}`
  return `总计 ${group.account_total} 个账号`
}

async function loadStatus(silent = false) {
  if (silent) refreshing.value = true
  else loading.value = true
  error.value = ''
  try {
    data.value = await api.get<ChannelStatusResponse>(`/api/user/channel-status?range=${encodeURIComponent(selectedRange.value)}`)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '渠道状态加载失败'
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

watch(selectedRange, () => { void loadStatus() })
onMounted(() => {
  void loadStatus()
  refreshTimer = window.setInterval(() => { void loadStatus(true) }, 60_000)
})
onBeforeUnmount(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
})
</script>

<template>
  <div class="channel-status-page">
    <header class="channel-page-head">
      <div><h1>渠道状态</h1><p>当前状态按最近实际请求判定；没有请求时使用巡检结果。</p></div>
      <button type="button" :disabled="refreshing" @click="loadStatus(true)">{{ refreshing ? '刷新中' : '刷新' }}</button>
    </header>

    <section class="channel-status-toolbar" aria-label="渠道状态摘要">
      <div class="channel-status-summary">
        <div class="channel-overall-state">
          <span class="channel-state-tag" :class="`is-${overallState}`"><i></i>{{ overallStateLabel(overallState) }}</span>
          <ul aria-label="各状态分组数量">
            <li><i class="is-healthy"></i>正常 {{ stateCounts.healthy || 0 }}</li>
            <li><i class="is-degraded"></i>波动 {{ stateCounts.degraded || 0 }}</li>
            <li><i class="is-down"></i>故障 {{ stateCounts.down || 0 }}</li>
            <li v-if="stateCounts.unknown"><i class="is-unknown"></i>待探测 {{ stateCounts.unknown }}</li>
            <li v-if="stateCounts.disabled"><i class="is-disabled"></i>停用 {{ stateCounts.disabled }}</li>
          </ul>
        </div>
        <dl>
          <div><dt>巡检频率</dt><dd>{{ formatInterval(data?.probe_interval_seconds) }}</dd></div>
          <div><dt>最近巡检</dt><dd :title="formatDate(data?.last_probe_at)">{{ formatRelative(data?.last_probe_at) }}</dd></div>
          <div><dt>数据更新</dt><dd :title="formatDate(data?.generated_at)">{{ formatRelative(data?.generated_at) }}</dd></div>
          <div v-if="data?.admin_view"><dt>范围</dt><dd>全部分组</dd></div>
        </dl>
      </div>
      <div class="channel-range-tabs" aria-label="请求统计时间范围">
        <button v-for="item in ranges" :key="item.value" type="button" :aria-pressed="selectedRange === item.value" :class="{ 'is-active': selectedRange === item.value }" @click="selectedRange = item.value">{{ item.label }}</button>
      </div>
    </section>

    <div v-if="error" class="channel-error-state"><span>{{ error }}</span><button type="button" @click="loadStatus()">重试</button></div>

    <div v-if="loading" class="channel-loading-grid" aria-label="正在加载渠道状态">
      <span v-for="item in 5" :key="item"></span>
    </div>

    <template v-else-if="groupedChannels.length">
      <section v-for="[platform, items] in groupedChannels" :key="platform" class="channel-platform-section">
        <header>
          <div><ProviderLogo :platform="platform" size="sm" /><h2>{{ platformFamily(platform) }}</h2></div>
          <span>{{ items.length }} 个分组</span>
        </header>
        <div class="channel-card-grid">
          <div class="channel-list-head" aria-hidden="true">
            <span>渠道</span><span>当前状态</span><span>{{ selectedRangeLabel }}请求</span><span>账号巡检</span><span>首字耗时</span><span>巡检记录</span>
          </div>
          <article v-for="group in items" :key="group.id" class="channel-card" :class="`is-${group.state}`">
            <header class="channel-card-head">
              <ProviderLogo class="channel-platform-mark" :platform="group.platform" size="md" />
              <div>
                <div><h3 :title="group.name">{{ group.name }}</h3><span v-if="data?.admin_view && !group.is_public" class="channel-private-tag">私有</span></div>
                <p><code :title="group.top_model">{{ group.top_model || '暂无请求模型' }}</code><span v-if="group.last_request_at" :title="formatDate(group.last_request_at)">请求于 {{ formatRelative(group.last_request_at) }}</span></p>
              </div>
            </header>

            <div class="channel-current-state">
              <span class="channel-state-tag" :class="`is-${group.state}`"><i></i>{{ stateLabel(group.state) }}</span>
              <small>{{ stateSourceLabel(group.state_source) }} · {{ currentEvidence(group) }}</small>
            </div>

            <div class="channel-request-rate">
              <strong :class="rateClass(group.request_success_rate, group.request_total)">{{ formatRate(group.request_success_rate, group.request_total) }}</strong>
              <span v-if="group.request_total">{{ group.request_successes }} / {{ group.request_total }} 次成功</span>
              <span v-else>区间内暂无有效请求</span>
            </div>

            <div class="channel-account-state">
              <strong>{{ accountValue(group) }}</strong>
              <span>{{ accountDetail(group) }}</span>
            </div>

            <div class="channel-latency-state">
              <strong>{{ formatLatency(group.average_ttft_ms) }}</strong>
              <span>首字耗时 · 巡检 {{ formatLatency(group.average_probe_latency_ms) }}</span>
            </div>

            <div class="channel-timeline" :aria-label="`${group.name} ${data?.range || ''} 巡检记录`">
              <div><span>过去</span><span :title="formatDate(group.last_probe_at)">最近 {{ formatRelative(group.last_probe_at) }}</span></div>
              <div class="channel-timeline-bars" aria-hidden="true">
                <i v-for="(bucket, index) in group.timeline" :key="`${bucket.at}-${index}`" :class="`is-${bucket.state}`" :title="`${formatDate(bucket.at)} ${stateLabel(bucket.state)}`"></i>
              </div>
              <small>{{ formatRate(group.probe_success_rate, group.probe_total) }} · {{ group.probe_successes }}/{{ group.probe_total }} 次巡检成功</small>
            </div>
          </article>
        </div>
      </section>
    </template>

    <div v-else-if="!error" class="channel-empty-state">暂无可见分组</div>
  </div>
</template>

<style scoped>
.channel-status-page { display: grid; width: 100%; gap: 1rem; color: var(--ink); }
.channel-page-head { display: flex; min-height: 3.5rem; align-items: flex-start; justify-content: space-between; gap: 1rem; padding-bottom: .85rem; border-bottom: 1px solid var(--line); }
.channel-page-head h1 { font-size: 1.08rem; font-weight: 880; letter-spacing: -.02em; }
.channel-page-head p { max-width: 34rem; margin-top: .22rem; color: var(--ink-soft); font-size: .65rem; line-height: 1.5; }
.channel-page-head > button,
.channel-error-state button { min-height: 2.3rem; padding: 0 .82rem; border: 1px solid var(--line); border-radius: .48rem; background: var(--surface); color: var(--ink); font-size: .68rem; font-weight: 780; }
.channel-page-head > button:hover,
.channel-error-state button:hover { border-color: color-mix(in srgb, var(--accent) 65%, var(--line)); }
.channel-page-head > button:disabled { opacity: .5; }
.channel-status-toolbar { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 1rem; padding: .78rem .9rem; border: 1px solid var(--line); border-radius: .65rem; background: var(--surface); }
.channel-status-summary { display: grid; min-width: 0; gap: .52rem; }
.channel-overall-state { display: flex; min-width: 0; align-items: center; gap: .75rem; }
.channel-overall-state ul { display: flex; flex-wrap: wrap; gap: .35rem .75rem; color: var(--ink-soft); font-size: .61rem; }
.channel-overall-state li { display: inline-flex; align-items: center; gap: .28rem; white-space: nowrap; }
.channel-overall-state li i { width: .38rem; height: .38rem; border-radius: 50%; background: var(--line-strong); }
.channel-overall-state li i.is-healthy { background: rgb(var(--dd-signal-green)); }
.channel-overall-state li i.is-degraded { background: rgb(var(--dd-amber-dim)); }
.channel-overall-state li i.is-down { background: rgb(var(--dd-signal-red)); }
.channel-status-summary dl { display: flex; flex-wrap: wrap; gap: .35rem 1rem; }
.channel-status-summary dl div { display: flex; align-items: center; gap: .3rem; color: var(--ink-soft); font-size: .59rem; }
.channel-status-summary dt::after { content: ':'; }
.channel-status-summary dd { color: var(--ink); }
.channel-range-tabs { display: flex; flex: 0 0 auto; gap: .25rem; padding: .18rem; border: 1px solid var(--line); border-radius: .52rem; background: var(--surface-muted); }
.channel-range-tabs button { min-height: 1.8rem; padding: 0 .62rem; border: 0; border-radius: .36rem; background: transparent; color: var(--ink-soft); font-size: .62rem; font-weight: 760; }
.channel-range-tabs button:hover { color: var(--ink); }
.channel-range-tabs button.is-active { background: var(--surface); box-shadow: 0 1px 3px var(--shadow); color: var(--ink); }
.channel-state-tag { display: inline-flex; min-height: 1.52rem; align-items: center; justify-content: center; gap: .34rem; padding: 0 .5rem; border-radius: 999px; background: var(--surface-muted); color: var(--ink-soft); font-size: .59rem; font-weight: 820; white-space: nowrap; }
.channel-state-tag > i { width: .36rem; height: .36rem; border-radius: 50%; background: currentColor; box-shadow: 0 0 0 2px color-mix(in srgb, currentColor 14%, transparent); }
.channel-state-tag.is-healthy { background: color-mix(in srgb, var(--surface) 84%, rgb(var(--dd-signal-green))); color: rgb(var(--dd-signal-green)); }
.channel-state-tag.is-degraded { background: var(--accent-soft); color: rgb(var(--dd-amber-dim)); }
.channel-state-tag.is-down,
.channel-state-tag.is-expired { background: color-mix(in srgb, var(--surface) 86%, rgb(var(--dd-signal-red))); color: rgb(var(--dd-signal-red)); }
.channel-platform-section { display: grid; gap: .45rem; }
.channel-platform-section > header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .15rem .2rem; }
.channel-platform-section > header > div { display: flex; align-items: center; gap: .45rem; }
.channel-platform-section > header h2 { font-size: .78rem; font-weight: 850; }
.channel-platform-section > header > span { color: var(--ink-soft); font-size: .59rem; }
.channel-card-grid { overflow: hidden; border: 1px solid var(--line); border-radius: .66rem; background: var(--surface); }
.channel-list-head,
.channel-card { display: grid; min-width: 0; grid-template-columns: minmax(13rem, 1.3fr) minmax(8rem, .72fr) minmax(8.5rem, .8fr) minmax(7rem, .62fr) minmax(8rem, .68fr) minmax(10rem, 1.05fr); gap: .72rem; }
.channel-list-head { align-items: center; padding: .5rem .75rem; border-bottom: 1px solid var(--line); background: var(--surface-muted); color: var(--ink-soft); font-size: .54rem; font-weight: 760; }
.channel-card { align-items: center; padding: .68rem .75rem; border-bottom: 1px solid var(--line); background: var(--surface); }
.channel-card:last-child { border-bottom: 0; }
.channel-card:hover { background: color-mix(in srgb, var(--surface-muted) 52%, var(--surface)); }
.channel-card-head { display: flex; min-width: 0; align-items: center; gap: .55rem; }
.channel-platform-mark { flex: 0 0 auto; }
.channel-card-head > div { display: grid; min-width: 0; flex: 1; gap: .25rem; }
.channel-card-head > div > div { display: flex; min-width: 0; align-items: center; gap: .4rem; }
.channel-card-head h3 { overflow: hidden; font-size: .71rem; font-weight: 850; text-overflow: ellipsis; white-space: nowrap; }
.channel-private-tag { padding: .13rem .3rem; border-radius: .3rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .52rem; font-weight: 780; }
.channel-card-head p { display: flex; min-width: 0; align-items: center; gap: .4rem; color: var(--ink-soft); font-size: .54rem; }
.channel-card-head code { max-width: 9rem; overflow: hidden; font-size: .55rem; text-overflow: ellipsis; white-space: nowrap; }
.channel-card-head p span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.channel-current-state,
.channel-request-rate,
.channel-account-state,
.channel-latency-state { display: grid; min-width: 0; align-content: center; justify-items: start; gap: .2rem; }
.channel-current-state small,
.channel-request-rate span,
.channel-account-state span,
.channel-latency-state span { overflow: hidden; max-width: 100%; color: var(--ink-soft); font-size: .53rem; line-height: 1.35; text-overflow: ellipsis; white-space: nowrap; }
.channel-request-rate strong,
.channel-account-state strong,
.channel-latency-state strong { font-size: .71rem; font-weight: 830; letter-spacing: -.01em; }
.channel-request-rate strong.is-good { color: rgb(var(--dd-signal-green)); }
.channel-request-rate strong.is-warning { color: rgb(var(--dd-amber-dim)); }
.channel-request-rate strong.is-bad { color: rgb(var(--dd-signal-red)); }
.channel-request-rate strong.is-unknown { color: var(--ink-soft); }
.channel-timeline { display: grid; min-width: 0; gap: .22rem; }
.channel-timeline > div:first-child { display: flex; justify-content: space-between; color: var(--ink-soft); font-size: .5rem; }
.channel-timeline-bars { display: grid; grid-template-columns: repeat(60, minmax(0, 1fr)); gap: 1px; height: .72rem; }
.channel-timeline-bars i { min-width: 0; border-radius: .08rem; background: var(--line); }
.channel-timeline-bars i.is-healthy { background: rgb(var(--dd-signal-green)); }
.channel-timeline-bars i.is-degraded { background: rgb(var(--dd-amber-dim)); }
.channel-timeline-bars i.is-down,
.channel-timeline-bars i.is-expired { background: rgb(var(--dd-signal-red)); }
.channel-timeline small { overflow: hidden; color: var(--ink-soft); font-size: .5rem; text-overflow: ellipsis; white-space: nowrap; }
.channel-error-state,
.channel-empty-state { display: flex; min-height: 9rem; align-items: center; justify-content: center; gap: .7rem; border: 1px solid var(--line); border-radius: .65rem; background: var(--surface); color: var(--ink-soft); font-size: .67rem; }
.channel-loading-grid { display: grid; gap: 1px; overflow: hidden; border: 1px solid var(--line); border-radius: .66rem; background: var(--line); }
.channel-loading-grid span { min-height: 4.2rem; background: var(--surface-muted); animation: channel-skeleton 1.1s ease-in-out infinite alternate; }
.channel-status-page :is(button, a):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }

@keyframes channel-skeleton { from { opacity: .55; } to { opacity: 1; } }

@media (max-width: 1240px) {
  .channel-list-head { display: none; }
  .channel-card-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1px; background: var(--line); }
  .channel-card { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .65rem .8rem; border: 0; }
  .channel-card-head { grid-column: 1 / 3; }
  .channel-current-state { align-items: end; justify-items: end; text-align: right; }
  .channel-request-rate,
  .channel-account-state,
  .channel-latency-state { padding-top: .5rem; border-top: 1px solid var(--line); }
  .channel-timeline { grid-column: 1 / -1; }
}

@media (max-width: 860px) {
  .channel-status-toolbar { align-items: stretch; flex-direction: column; }
  .channel-range-tabs { align-self: flex-start; }
  .channel-card-grid { grid-template-columns: 1fr; }
}

@media (max-width: 560px) {
  .channel-page-head { align-items: stretch; flex-direction: column; }
  .channel-page-head > button { width: 100%; }
  .channel-overall-state { align-items: flex-start; flex-direction: column; }
  .channel-status-summary dl { display: grid; grid-template-columns: 1fr 1fr; }
  .channel-range-tabs { display: grid; width: 100%; grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .channel-card { grid-template-columns: 1fr 1fr; padding: .7rem; }
  .channel-card-head { grid-column: 1 / -1; }
  .channel-current-state { align-items: start; justify-items: start; padding-top: .5rem; border-top: 1px solid var(--line); text-align: left; }
  .channel-request-rate { align-items: end; justify-items: end; text-align: right; }
  .channel-account-state,
  .channel-latency-state { padding-top: .45rem; }
  .channel-timeline { padding-top: .15rem; }
  .channel-timeline-bars { height: .82rem; }
}

@media (prefers-reduced-motion: reduce) {
  .channel-loading-grid span { animation: none; }
}
</style>
