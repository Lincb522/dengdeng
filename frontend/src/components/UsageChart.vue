<script setup lang="ts">
import { computed, ref } from 'vue'
import type { DailyRow } from '../api/types'
import { formatMoney, formatTokens } from '../api/types'

const props = defineProps<{ daily: DailyRow[] }>()

const W = 920
const H = 292
const PAD = { top: 18, right: 64, bottom: 42, left: 64 }
const TICK_COUNT = 5
const activeIndex = ref<number | null>(null)

function axisValue(value: number) {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(value >= 10_000_000_000 ? 0 : 1)}B`
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

const rows = computed(() => props.daily || [])
const plotWidth = W - PAD.left - PAD.right
const plotHeight = H - PAD.top - PAD.bottom
const tokenMax = computed(() => niceAxisMax(Math.max(...rows.value.map((row) => row.tokens), 1)))
const requestMax = computed(() => niceAxisMax(Math.max(...rows.value.map((row) => row.requests), TICK_COUNT)))
const columnWidth = computed(() => plotWidth / Math.max(rows.value.length, 1))

const points = computed(() => rows.value.map((row, index) => {
  const column = columnWidth.value
  const barWidth = Math.min(34, column * 0.52)
  const tokenHeight = row.tokens > 0 ? Math.max(3, (row.tokens / tokenMax.value) * plotHeight) : 0
  const x = PAD.left + index * column + column / 2
  return {
    ...row,
    x,
    barX: x - barWidth / 2,
    barWidth,
    tokenY: PAD.top + plotHeight - tokenHeight,
    tokenHeight,
    requestY: PAD.top + plotHeight - (row.requests / requestMax.value) * plotHeight,
  }
}))

const requestLine = computed(() => points.value.map((point, index) => `${index ? 'L' : 'M'} ${point.x} ${point.requestY}`).join(' '))
const gridTicks = computed(() => Array.from({ length: TICK_COUNT + 1 }, (_, index) => {
  const ratio = index / TICK_COUNT
  return {
    y: PAD.top + plotHeight * ratio,
    tokens: tokenMax.value * (1 - ratio),
    requests: requestMax.value * (1 - ratio),
  }
}))

const totalTokens = computed(() => rows.value.reduce((total, row) => total + row.tokens, 0))
const totalRequests = computed(() => rows.value.reduce((total, row) => total + row.requests, 0))
const totalCost = computed(() => rows.value.reduce((total, row) => total + row.cost_micro, 0))
const peakIndex = computed(() => {
  if (!rows.value.length) return -1
  return rows.value.reduce((peak, row, index) => row.tokens > rows.value[peak].tokens ? index : peak, 0)
})
const selectedIndex = computed(() => activeIndex.value ?? peakIndex.value)
const selected = computed(() => selectedIndex.value >= 0 ? rows.value[selectedIndex.value] : null)
const averageTokens = computed(() => rows.value.length ? Math.round(totalTokens.value / rows.value.length) : 0)
</script>

<template>
  <section class="data-chart usage-chart card" aria-labelledby="usage-chart-title">
    <header class="data-chart-head">
      <div>
        <h3 id="usage-chart-title">近 14 天调用趋势</h3>
        <p>Token 使用量与请求数采用左右独立刻度。</p>
      </div>
      <div class="data-chart-legend" aria-label="图例">
        <span><i class="is-token"></i>Token · 左轴</span>
        <span><i class="is-request"></i>请求 · 右轴</span>
      </div>
    </header>

    <dl class="data-chart-summary">
      <div><dt>Token 总量</dt><dd>{{ formatTokens(totalTokens) }}</dd></div>
      <div><dt>请求总量</dt><dd>{{ totalRequests.toLocaleString() }}</dd></div>
      <div><dt>日均 Token</dt><dd>{{ formatTokens(averageTokens) }}</dd></div>
      <div><dt>账面费用</dt><dd>{{ formatMoney(totalCost) }}</dd></div>
    </dl>

    <div v-if="!points.length" class="data-chart-empty">这段时间还没有调用记录</div>
    <div v-else class="data-chart-scroll" @mouseleave="activeIndex = null">
      <svg :viewBox="`0 0 ${W} ${H}`" class="data-chart-canvas" role="img" aria-label="近十四天 Token 柱状图和请求趋势图">
        <g v-for="tick in gridTicks" :key="tick.y">
          <line class="data-chart-grid" :x1="PAD.left" :x2="W - PAD.right" :y1="tick.y" :y2="tick.y" />
          <text class="data-chart-axis" :x="PAD.left - 12" :y="tick.y + 4" text-anchor="end">{{ axisValue(tick.tokens) }}</text>
          <text class="data-chart-axis" :x="W - PAD.right + 12" :y="tick.y + 4">{{ axisValue(tick.requests) }}</text>
        </g>

        <g v-for="(point, index) in points" :key="point.day">
          <rect v-if="selectedIndex === index" class="data-chart-focus-band" :x="point.x - columnWidth / 2 + 2" :y="PAD.top" :width="columnWidth - 4" :height="plotHeight" rx="5" />
          <rect class="data-chart-token-bar" :x="point.barX" :y="point.tokenY" :width="point.barWidth" :height="point.tokenHeight" rx="4" />
          <text class="data-chart-axis" :x="point.x" :y="H - 14" text-anchor="middle">{{ point.day.slice(5) }}</text>
        </g>

        <path v-if="requestLine" class="data-chart-request-line" :d="requestLine" />
        <circle v-for="(point, index) in points" :key="`request-${point.day}`" class="data-chart-request-point" :class="{ 'is-active': selectedIndex === index }" :cx="point.x" :cy="point.requestY" :r="selectedIndex === index ? 4 : 2.5" />

        <rect
          v-for="(point, index) in points"
          :key="`hit-${point.day}`"
          class="data-chart-hit"
          :x="point.x - columnWidth / 2"
          :y="PAD.top"
          :width="columnWidth"
          :height="plotHeight"
          tabindex="0"
          :aria-label="`${point.day}，${formatTokens(point.tokens)} Token，${point.requests} 次请求，${formatMoney(point.cost_micro)}`"
          @mouseenter="activeIndex = index"
          @focus="activeIndex = index"
        />
      </svg>
    </div>

    <dl v-if="selected" class="data-chart-detail" aria-live="polite">
      <div><dt>日期</dt><dd>{{ selected.day }}</dd></div>
      <div><dt>Token</dt><dd>{{ formatTokens(selected.tokens) }}</dd></div>
      <div><dt>请求</dt><dd>{{ selected.requests.toLocaleString() }} 次</dd></div>
      <div><dt>费用</dt><dd>{{ formatMoney(selected.cost_micro) }}</dd></div>
    </dl>
  </section>
</template>
