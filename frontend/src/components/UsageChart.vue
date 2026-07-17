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
</script>

<template>
  <div class="usage-chart card p-5">
    <div class="mb-4 flex items-baseline justify-between">
      <h3 class="text-sm font-semibold text-slate-200">近 14 天 Token 用量</h3>
      <span class="font-mono text-[10px] uppercase tracking-widest text-slate-500">tokens / day</span>
    </div>
    <svg :viewBox="`0 0 ${W} ${H}`" class="w-full">
      <line v-for="i in 3" :key="i" class="chart-grid-line" :x1="PAD" :x2="W - PAD" :y1="((H - 24) / 4) * i" :y2="((H - 24) / 4) * i" stroke-width="1" />
      <g v-for="(b, i) in bars" :key="i">
        <rect class="chart-bar" :x="b.x" :y="b.y" :width="b.w" :height="b.h" rx="3" opacity="0.88">
          <title>{{ b.row.day }}: {{ formatTokens(b.row.tokens) }} tokens / {{ b.row.requests }} 次 / {{ formatMoney(b.row.cost_micro) }}</title>
        </rect>
        <text class="chart-axis-label" :x="b.x + b.w / 2" :y="H - 8" text-anchor="middle" font-size="10" font-family="monospace">
          {{ b.row.day.slice(3) }}
        </text>
      </g>
    </svg>
  </div>
</template>
