<script setup lang="ts">
import { computed, ref } from 'vue'
import type { OpsTrend } from '../api/types'
import { formatMoney, formatTokens } from '../api/types'

const props = defineProps<{ items: OpsTrend[] }>()

const W = 920
const H = 292
const PAD = { top: 18, right: 26, bottom: 42, left: 64 }
const TICK_COUNT = 5
const activeIndex = ref<number | null>(null)

function axisValue(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(value >= 10_000 ? 0 : 1)}K`
  return Math.round(value).toLocaleString()
}

function niceAxisMax(value: number) {
  const safe = Math.max(value, 1)
  const magnitude = 10 ** Math.floor(Math.log10(safe))
  const normalized = safe / magnitude
  const step = [1, 2, 2.5, 3, 4, 5, 8, 10].find((candidate) => normalized <= candidate) || 10
  return step * magnitude
}

function formatLatency(milliseconds: number) {
  if (!milliseconds) return '—'
  if (milliseconds < 1000) return `${Math.round(milliseconds)}ms`
  return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 2 : 1)}s`
}

const rows = computed(() => props.items || [])
const plotWidth = W - PAD.left - PAD.right
const plotHeight = H - PAD.top - PAD.bottom
const maxRequests = computed(() => niceAxisMax(Math.max(...rows.value.map((row) => row.requests), TICK_COUNT)))
const columnWidth = computed(() => plotWidth / Math.max(rows.value.length, 1))
const points = computed(() => rows.value.map((row, index) => {
  const column = columnWidth.value
  const barWidth = Math.min(30, Math.max(8, column * 0.56))
  const totalHeight = row.requests > 0 ? Math.max(3, (row.requests / maxRequests.value) * plotHeight) : 0
  const errorHeight = row.requests > 0 ? totalHeight * (row.error_requests / row.requests) : 0
  const x = PAD.left + index * column + column / 2
  return {
    ...row,
    x,
    barX: x - barWidth / 2,
    barWidth,
    totalY: PAD.top + plotHeight - totalHeight,
    totalHeight,
    errorY: PAD.top + plotHeight - totalHeight,
    errorHeight,
  }
}))
const gridTicks = computed(() => Array.from({ length: TICK_COUNT + 1 }, (_, index) => {
  const ratio = index / TICK_COUNT
  return { y: PAD.top + plotHeight * ratio, value: maxRequests.value * (1 - ratio) }
}))
const labelStep = computed(() => Math.max(1, Math.ceil(points.value.length / 7)))
const totalRequests = computed(() => rows.value.reduce((sum, row) => sum + row.requests, 0))
const totalErrors = computed(() => rows.value.reduce((sum, row) => sum + row.error_requests, 0))
const totalTokens = computed(() => rows.value.reduce((sum, row) => sum + row.tokens, 0))
const weightedLatency = computed(() => {
  if (!totalRequests.value) return 0
  return rows.value.reduce((sum, row) => sum + row.average_latency_ms * row.requests, 0) / totalRequests.value
})
const peakIndex = computed(() => {
  if (!rows.value.length) return -1
  return rows.value.reduce((peak, row, index) => row.requests > rows.value[peak].requests ? index : peak, 0)
})
const selectedIndex = computed(() => activeIndex.value ?? peakIndex.value)
const selected = computed(() => selectedIndex.value >= 0 ? rows.value[selectedIndex.value] : null)
const successRate = computed(() => totalRequests.value ? ((totalRequests.value - totalErrors.value) / totalRequests.value) * 100 : 100)
</script>

<template>
  <section class="data-chart ops-chart card" aria-labelledby="ops-chart-title">
    <header class="data-chart-head">
      <div>
        <h3 id="ops-chart-title">请求走势</h3>
        <p>每根柱代表一个时间段，红色部分为失败请求。</p>
      </div>
      <div class="data-chart-legend" aria-label="图例">
        <span><i class="is-success"></i>成功</span>
        <span><i class="is-error"></i>失败</span>
      </div>
    </header>

    <dl class="data-chart-summary">
      <div><dt>请求总量</dt><dd>{{ totalRequests.toLocaleString() }}</dd></div>
      <div><dt>成功率</dt><dd>{{ successRate.toFixed(2) }}%</dd></div>
      <div><dt>失败请求</dt><dd>{{ totalErrors.toLocaleString() }}</dd></div>
      <div><dt>平均耗时</dt><dd>{{ formatLatency(weightedLatency) }}</dd></div>
    </dl>

    <div v-if="!points.length" class="data-chart-empty">这个时间段还没有调用记录</div>
    <div v-else class="data-chart-scroll" @mouseleave="activeIndex = null">
      <svg :viewBox="`0 0 ${W} ${H}`" class="data-chart-canvas" role="img" aria-label="请求成功和失败数量柱状图">
        <g v-for="tick in gridTicks" :key="tick.y">
          <line class="data-chart-grid" :x1="PAD.left" :x2="W - PAD.right" :y1="tick.y" :y2="tick.y" />
          <text class="data-chart-axis" :x="PAD.left - 12" :y="tick.y + 4" text-anchor="end">{{ axisValue(tick.value) }}</text>
        </g>

        <g v-for="(point, index) in points" :key="point.start">
          <rect v-if="selectedIndex === index" class="data-chart-focus-band" :x="point.x - columnWidth / 2 + 1" :y="PAD.top" :width="columnWidth - 2" :height="plotHeight" rx="5" />
          <rect class="data-chart-success-bar" :x="point.barX" :y="point.totalY" :width="point.barWidth" :height="point.totalHeight" rx="4" />
          <rect v-if="point.errorHeight" class="data-chart-error-bar" :x="point.barX" :y="point.errorY" :width="point.barWidth" :height="Math.max(2, point.errorHeight)" rx="4" />
          <text v-if="index % labelStep === 0 || index === points.length - 1" class="data-chart-axis" :x="point.x" :y="H - 14" text-anchor="middle">{{ point.label }}</text>
        </g>

        <rect
          v-for="(point, index) in points"
          :key="`hit-${point.start}`"
          class="data-chart-hit"
          :x="point.x - columnWidth / 2"
          :y="PAD.top"
          :width="columnWidth"
          :height="plotHeight"
          tabindex="0"
          :aria-label="`${point.label}，${point.requests} 次请求，${point.error_requests} 次失败，${formatTokens(point.tokens)} Token`"
          @mouseenter="activeIndex = index"
          @focus="activeIndex = index"
        />
      </svg>
    </div>

    <dl v-if="selected" class="data-chart-detail" aria-live="polite">
      <div><dt>时间段</dt><dd>{{ selected.label }}</dd></div>
      <div><dt>请求 / 失败</dt><dd>{{ selected.requests.toLocaleString() }} / {{ selected.error_requests.toLocaleString() }}</dd></div>
      <div><dt>Token</dt><dd>{{ formatTokens(selected.tokens) }}</dd></div>
      <div><dt>平均耗时 / 费用</dt><dd>{{ formatLatency(selected.average_latency_ms) }} · {{ formatMoney(selected.cost_micro) }}</dd></div>
    </dl>
  </section>
</template>
