<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { ChannelGroupStatus, ChannelStatusResponse } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'

const ranges = [
  { value: '1h', label: '1 小时' },
  { value: '3h', label: '3 小时' },
  { value: '24h', label: '1 天' },
  { value: '7d', label: '7 天' },
  { value: '15d', label: '15 天' },
]
const platformOrder = ['openai', 'anthropic', 'gemini', 'grok']
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
const overallState = computed(() => {
  if (!groups.value.length) return 'unknown'
  if (groups.value.some((group) => group.state === 'down')) return 'down'
  if (groups.value.some((group) => group.state === 'degraded')) return 'degraded'
  if (groups.value.some((group) => group.state === 'unknown')) return 'unknown'
  if (groups.value.every((group) => group.state === 'disabled')) return 'disabled'
  return 'healthy'
})

function platformLabel(value: string) {
  return PLATFORM_LABELS[value] || value
}

function platformFamily(value: string) {
  return ({ openai: 'GPT 系列', anthropic: 'Claude 系列', gemini: 'Gemini 系列', grok: 'Grok 系列' } as Record<string, string>)[value] || `${platformLabel(value)} 系列`
}

function stateLabel(value: string) {
  return ({ healthy: '正常', degraded: '波动', down: '故障', expired: '已过期', disabled: '已停用', unknown: '待探测' } as Record<string, string>)[value] || value
}

function formatDate(value?: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '暂无数据'
}

function formatLatency(value: number) {
  return value > 0 ? `${Math.round(value)} ms` : '暂无数据'
}

function formatRate(value: number, total: number) {
  return total > 0 ? `${value.toFixed(2)}%` : '暂无'
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
      <div><h1>可用渠道</h1><p>自动探测与实际请求统计</p></div>
      <button type="button" :disabled="refreshing" @click="loadStatus(true)">{{ refreshing ? '刷新中' : '刷新' }}</button>
    </header>

    <section class="channel-status-toolbar" aria-label="渠道状态摘要">
      <div class="channel-status-summary">
        <div><span class="channel-state-tag" :class="`is-${overallState}`">{{ stateLabel(overallState) }}</span><strong>共 {{ groups.length }} 个监控分组</strong></div>
        <dl>
          <div><dt>最近探测</dt><dd>{{ formatDate(data?.last_probe_at) }}</dd></div>
          <div><dt>更新时间</dt><dd>{{ formatDate(data?.generated_at) }}</dd></div>
          <div v-if="data?.admin_view"><dt>可见范围</dt><dd>全部分组</dd></div>
        </dl>
      </div>
      <div class="channel-range-tabs" aria-label="统计时间范围">
        <button v-for="item in ranges" :key="item.value" type="button" :class="{ 'is-active': selectedRange === item.value }" @click="selectedRange = item.value">{{ item.label }}</button>
      </div>
    </section>

    <div v-if="error" class="channel-error-state"><span>{{ error }}</span><button type="button" @click="loadStatus()">重试</button></div>

    <div v-if="loading" class="channel-loading-grid" aria-label="正在加载渠道状态">
      <span v-for="item in 4" :key="item"></span>
    </div>

    <template v-else-if="groupedChannels.length">
      <section v-for="[platform, items] in groupedChannels" :key="platform" class="channel-platform-section">
        <header><h2>{{ platformFamily(platform) }}</h2><span>{{ items.length }} 个监控分组</span></header>
        <div class="channel-card-grid">
          <article v-for="group in items" :key="group.id" class="channel-card" :class="`is-${group.state}`">
            <header class="channel-card-head">
              <span class="channel-platform-mark" aria-hidden="true">{{ platformLabel(group.platform).slice(0, 1) }}</span>
              <div>
                <div><h3 :title="group.name">{{ group.name }}</h3><span v-if="data?.admin_view && !group.is_public" class="channel-private-tag">私有</span></div>
                <p><span>{{ platformFamily(group.platform) }}</span><code :title="group.top_model">{{ group.top_model || '暂无请求模型' }}</code></p>
              </div>
              <span class="channel-state-tag" :class="`is-${group.state}`">{{ stateLabel(group.state) }}</span>
            </header>

            <dl class="channel-card-metrics">
              <div><dt>首字延迟</dt><dd>{{ formatLatency(group.average_ttft_ms) }}</dd></div>
              <div><dt>最近探测</dt><dd>{{ formatDate(group.last_probe_at) }}</dd></div>
              <div><dt>探测延迟</dt><dd>{{ formatLatency(group.average_probe_latency_ms) }}</dd></div>
              <div><dt>可用账号</dt><dd>{{ group.account_available }} / {{ group.account_total }}</dd></div>
            </dl>

            <div class="channel-card-rates">
              <div><span>探测可用率</span><strong class="is-availability">{{ formatRate(group.probe_success_rate, group.probe_total) }}</strong><small>{{ group.probe_successes }}/{{ group.probe_total }} 次探测成功</small></div>
              <div><span>请求成功率</span><strong class="is-success">{{ formatRate(group.request_success_rate, group.request_total) }}</strong><small>{{ group.request_successes }}/{{ group.request_total }} 次请求成功</small></div>
            </div>

            <div class="channel-timeline" :aria-label="`${group.name} ${data?.range || ''} 探测时间线`">
              <div><span>过去</span><span>现在</span></div>
              <div class="channel-timeline-bars" aria-hidden="true">
                <i v-for="(bucket, index) in group.timeline" :key="`${bucket.at}-${index}`" :class="`is-${bucket.state}`" :title="`${formatDate(bucket.at)} ${stateLabel(bucket.state)}`"></i>
              </div>
            </div>
          </article>
        </div>
      </section>
    </template>

    <div v-else-if="!error" class="channel-empty-state">暂无可见的监控分组</div>
  </div>
</template>

<style scoped>
.channel-status-page { display: grid; width: 100%; gap: 1rem; color: var(--ink); }
.channel-page-head { display: flex; min-height: 3.5rem; align-items: flex-start; justify-content: space-between; gap: 1rem; padding-bottom: .9rem; border-bottom: 1px solid var(--line); }
.channel-page-head h1 { font-size: 1.08rem; font-weight: 880; letter-spacing: -.02em; }
.channel-page-head p { margin-top: .22rem; color: var(--ink-soft); font-size: .68rem; }
.channel-page-head > button,
.channel-error-state button { min-height: 2.35rem; padding: 0 .85rem; border: 1px solid var(--line); border-radius: .5rem; background: var(--surface); color: var(--ink); font-size: .7rem; font-weight: 780; }
.channel-page-head > button:disabled { opacity: .5; }
.channel-status-toolbar { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 1.2rem; padding: .85rem 1rem; border: 1px solid var(--line); border-radius: .75rem; background: var(--surface); }
.channel-status-summary { display: grid; min-width: 0; gap: .55rem; }
.channel-status-summary > div { display: flex; min-width: 0; align-items: center; gap: .65rem; }
.channel-status-summary > div strong { color: var(--ink-soft); font-size: .72rem; font-weight: 720; }
.channel-status-summary dl { display: flex; flex-wrap: wrap; gap: .45rem 1.2rem; }
.channel-status-summary dl div { display: flex; align-items: center; gap: .35rem; color: var(--ink-soft); font-size: .63rem; }
.channel-status-summary dt::after { content: ':'; }
.channel-status-summary dd { color: var(--ink); }
.channel-range-tabs { display: flex; flex: 0 0 auto; flex-wrap: wrap; justify-content: flex-end; gap: .4rem; }
.channel-range-tabs button { min-height: 2rem; padding: 0 .68rem; border: 1px solid var(--line); border-radius: .48rem; background: var(--surface); color: var(--ink-soft); font-size: .65rem; font-weight: 780; }
.channel-range-tabs button.is-active { border-color: var(--accent); background: var(--accent); color: #251c11; }
.channel-state-tag { display: inline-flex; min-height: 1.65rem; align-items: center; justify-content: center; padding: 0 .58rem; border-radius: 999px; background: var(--surface-muted); color: var(--ink-soft); font-size: .62rem; font-weight: 820; white-space: nowrap; }
.channel-state-tag.is-healthy { background: color-mix(in srgb, var(--surface) 82%, rgb(var(--dd-signal-green))); color: rgb(var(--dd-signal-green)); }
.channel-state-tag.is-degraded { background: var(--accent-soft); color: rgb(var(--dd-amber-dim)); }
.channel-state-tag.is-down,
.channel-state-tag.is-expired { background: color-mix(in srgb, var(--surface) 86%, rgb(var(--dd-signal-red))); color: rgb(var(--dd-signal-red)); }
.channel-platform-section { display: grid; gap: .5rem; }
.channel-platform-section > header { display: flex; align-items: end; justify-content: space-between; gap: 1rem; padding: .25rem .25rem 0; }
.channel-platform-section > header h2 { font-size: .86rem; font-weight: 860; }
.channel-platform-section > header span { color: var(--ink-soft); font-size: .63rem; }
.channel-card-grid { display: grid; grid-template-columns: 1fr; gap: 1px; overflow: hidden; border: 1px solid var(--line); border-radius: .72rem; background: var(--line); }
.channel-card {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(14rem, 1.2fr) minmax(18rem, 1.35fr) minmax(12rem, .82fr) minmax(11rem, .9fr);
  align-items: center;
  gap: .85rem;
  padding: .72rem .8rem;
  border: 0;
  border-radius: 0;
  background: var(--surface);
}
.channel-card:hover { background: color-mix(in srgb, var(--surface-muted) 42%, var(--surface)); }
.channel-card-head { display: flex; min-width: 0; align-items: center; gap: .62rem; }
.channel-platform-mark { display: grid; width: 2rem; height: 2rem; flex: 0 0 auto; place-items: center; border-radius: .48rem; background: var(--surface-muted); color: var(--ink); font-size: .68rem; font-weight: 900; }
.channel-card-head > div { display: grid; min-width: 0; flex: 1; gap: .3rem; }
.channel-card-head > div > div { display: flex; min-width: 0; align-items: center; gap: .42rem; }
.channel-card-head h3 { overflow: hidden; font-size: .78rem; font-weight: 850; text-overflow: ellipsis; white-space: nowrap; }
.channel-private-tag { padding: .18rem .34rem; border-radius: .32rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .55rem; font-weight: 780; }
.channel-card-head p { display: flex; min-width: 0; align-items: center; gap: .45rem; }
.channel-card-head p span { color: var(--ink-soft); font-size: .55rem; font-weight: 730; white-space: nowrap; }
.channel-card-head code { overflow: hidden; color: var(--ink-soft); font-size: .59rem; text-overflow: ellipsis; white-space: nowrap; }
.channel-card-head > .channel-state-tag { min-height: 1.45rem; padding-inline: .46rem; }
.channel-card-metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); overflow: hidden; }
.channel-card-metrics div { display: grid; min-width: 0; gap: .18rem; padding: .18rem .55rem; border-right: 1px solid var(--line); }
.channel-card-metrics div:first-child { padding-left: 0; }
.channel-card-metrics div:last-child { padding-right: 0; border-right: 0; }
.channel-card-metrics dt { color: var(--ink-soft); font-size: .53rem; }
.channel-card-metrics dd { overflow: hidden; font-size: .64rem; font-weight: 810; text-overflow: ellipsis; white-space: nowrap; }
.channel-card-rates { display: grid; grid-template-columns: 1fr 1fr; gap: .65rem; }
.channel-card-rates > div { display: grid; min-width: 0; gap: .12rem; }
.channel-card-rates > div:last-child { justify-items: end; text-align: right; }
.channel-card-rates span { color: var(--ink-soft); font-size: .52rem; }
.channel-card-rates strong { font-size: .8rem; letter-spacing: -.02em; }
.channel-card-rates strong.is-availability { color: rgb(var(--dd-signal-green)); }
.channel-card-rates strong.is-success { color: rgb(var(--dd-signal-cyan)); }
.channel-card-rates small { display: none; }
.channel-timeline { display: grid; min-width: 0; gap: .28rem; }
.channel-timeline > div:first-child { display: flex; justify-content: space-between; color: var(--ink-soft); font-size: .55rem; font-weight: 780; }
.channel-timeline-bars { display: grid; grid-template-columns: repeat(60, minmax(0, 1fr)); gap: 1px; height: 1.12rem; }
.channel-timeline-bars i { min-width: 0; border-radius: .1rem; background: var(--line); }
.channel-timeline-bars i.is-healthy { background: rgb(var(--dd-signal-green)); }
.channel-timeline-bars i.is-degraded { background: var(--accent); }
.channel-timeline-bars i.is-down,
.channel-timeline-bars i.is-expired { background: rgb(var(--dd-signal-red)); }
.channel-error-state,
.channel-empty-state { display: flex; min-height: 11rem; align-items: center; justify-content: center; gap: .75rem; border: 1px solid var(--line); border-radius: .75rem; background: var(--surface); color: var(--ink-soft); font-size: .7rem; }
.channel-loading-grid { display: grid; gap: 1px; overflow: hidden; border: 1px solid var(--line); border-radius: .72rem; background: var(--line); }
.channel-loading-grid span { min-height: 4.6rem; background: var(--surface-muted); animation: channel-skeleton 1.1s ease-in-out infinite alternate; }
.channel-status-page :is(button, a):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }

@keyframes channel-skeleton { from { opacity: .55; } to { opacity: 1; } }

@media (max-width: 1320px) {
  .channel-card { grid-template-columns: minmax(14rem, 1fr) minmax(18rem, 1.1fr) minmax(11rem, .72fr); }
  .channel-timeline { grid-column: 1 / -1; padding-top: .52rem; border-top: 1px solid var(--line); }
}

@media (max-width: 760px) {
  .channel-status-toolbar { align-items: stretch; flex-direction: column; }
  .channel-range-tabs { justify-content: flex-start; }
  .channel-card { grid-template-columns: minmax(0, 1fr) minmax(11rem, .72fr); }
  .channel-card-metrics { grid-template-columns: 1fr 1fr; }
  .channel-card-metrics div:nth-child(2) { border-right: 0; }
  .channel-card-metrics div:nth-child(-n + 2) { padding-bottom: .38rem; }
  .channel-card-metrics div:nth-child(n + 3) { padding-top: .38rem; }
  .channel-card-metrics div:nth-child(3) { padding-left: 0; }
  .channel-card-rates,
  .channel-timeline { grid-column: 1 / -1; }
  .channel-card-rates { display: flex; justify-content: flex-end; border-top: 1px solid var(--line); padding-top: .48rem; }
  .channel-card-rates > div { display: flex; align-items: baseline; gap: .28rem; }
}

@media (max-width: 520px) {
  .channel-page-head { align-items: stretch; flex-direction: column; }
  .channel-page-head > button { width: 100%; }
  .channel-status-summary dl { display: grid; gap: .32rem; }
  .channel-range-tabs { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .channel-card { grid-template-columns: 1fr; padding: .7rem; }
  .channel-card-head { grid-column: 1; }
  .channel-card-head > .channel-state-tag { margin-left: auto; }
  .channel-card-metrics { grid-column: 1; padding-top: .5rem; border-top: 1px solid var(--line); }
  .channel-card-rates { gap: .6rem; }
  .channel-timeline-bars { height: 1rem; }
}

@media (prefers-reduced-motion: reduce) {
  .channel-loading-grid span { animation: none; }
}
</style>
