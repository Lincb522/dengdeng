<script setup lang="ts">
import { computed } from 'vue'
import type { DailyRow } from '../api/types'
import { formatMoney, formatTokens } from '../api/types'

const props = defineProps<{ daily: DailyRow[] }>()

// 纯 SVG 柱状图:tokens 柱 + 请求数标注,避免引入图表库。
const W = 720
const H = 180
const PAD = 8

const bars = computed(() => {
  const rows = props.daily || []
  if (!rows.length) return []
  const max = Math.max(...rows.map((r) => r.tokens), 1)
  const bw = (W - PAD * 2) / rows.length
  return rows.map((r, i) => {
    const h = Math.max((r.tokens / max) * (H - 40), r.tokens > 0 ? 3 : 0)
    return {
      x: PAD + i * bw + bw * 0.18,
      y: H - 24 - h,
      w: bw * 0.64,
      h,
      row: r,
    }
  })
})

const requestLine = computed(() => {
  const rows = props.daily || []
  if (!rows.length) return ''
  const maxRequests = Math.max(...rows.map((row) => row.requests), 1)
  const bw = (W - PAD * 2) / rows.length
  return rows.map((row, index) => {
    const x = PAD + index * bw + bw / 2
    const y = H - 24 - (row.requests / maxRequests) * (H - 40)
    return `${x},${y}`
  }).join(' ')
})

const totalTokens = computed(() => props.daily.reduce((total, row) => total + row.tokens, 0))
const totalRequests = computed(() => props.daily.reduce((total, row) => total + row.requests, 0))
</script>

<template>
  <div class="usage-chart card p-5">
    <div class="usage-chart-head">
      <div><h3>近 14 天调用趋势</h3><p>{{ formatTokens(totalTokens) }} Token · {{ totalRequests.toLocaleString() }} 次请求</p></div>
      <div class="usage-chart-legend"><span><i></i>Token</span><span><i></i>请求</span></div>
    </div>
    <svg :viewBox="`0 0 ${W} ${H}`" class="w-full" role="img" aria-label="近十四天 Token 和请求趋势">
      <line v-for="i in 3" :key="i" class="chart-grid-line" :x1="PAD" :x2="W - PAD" :y1="((H - 24) / 4) * i" :y2="((H - 24) / 4) * i" stroke-width="1" />
      <g v-for="(b, i) in bars" :key="i">
        <rect class="chart-bar" :x="b.x" :y="b.y" :width="b.w" :height="b.h" rx="3" opacity="0.88">
          <title>{{ b.row.day }}: {{ formatTokens(b.row.tokens) }} tokens / {{ b.row.requests }} 次 / {{ formatMoney(b.row.cost_micro) }}</title>
        </rect>
        <text class="chart-axis-label" :x="b.x + b.w / 2" :y="H - 8" text-anchor="middle" font-size="10" font-family="monospace">
          {{ b.row.day.slice(3) }}
        </text>
      </g>
      <polyline v-if="requestLine" class="usage-request-line" :points="requestLine" fill="none" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" />
    </svg>
  </div>
</template>
