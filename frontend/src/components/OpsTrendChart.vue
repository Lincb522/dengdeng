<script setup lang="ts">
import { computed } from 'vue'
import type { OpsTrend } from '../api/types'
import { formatTokens } from '../api/types'

const props = defineProps<{ items: OpsTrend[] }>()

const W = 820
const H = 230
const PAD = { top: 18, right: 12, bottom: 36, left: 8 }

const points = computed(() => {
  const rows = props.items || []
  if (!rows.length) return []
  const maxRequests = Math.max(...rows.map((row) => row.requests), 1)
  const maxErrors = Math.max(...rows.map((row) => row.error_requests), 1)
  const width = W - PAD.left - PAD.right
  const height = H - PAD.top - PAD.bottom
  return rows.map((row, index) => {
    const x = PAD.left + (rows.length === 1 ? width / 2 : (index / (rows.length - 1)) * width)
    return {
      ...row,
      x,
      requestY: PAD.top + height - (row.requests / maxRequests) * height,
      errorY: PAD.top + height - (row.error_requests / maxErrors) * height,
    }
  })
})

const requestLine = computed(() => points.value.map((point) => `${point.x},${point.requestY}`).join(' '))
const requestArea = computed(() => {
  const rows = points.value
  if (!rows.length) return ''
  const base = H - PAD.bottom
  return `${PAD.left},${base} ${rows.map((point) => `${point.x},${point.requestY}`).join(' ')} ${rows[rows.length - 1].x},${base}`
})
const visibleLabels = computed(() => {
  const rows = points.value
  const count = Math.min(6, rows.length)
  if (!count) return []
  return Array.from({ length: count }, (_, i) => rows[Math.round((i * (rows.length - 1)) / Math.max(1, count - 1))])
})
</script>

<template>
  <div class="ops-chart card p-5">
    <div class="mb-4 flex flex-wrap items-baseline justify-between gap-3">
      <div>
        <h3 class="text-sm font-semibold text-slate-200">请求走势</h3>
        <p class="mt-1 text-xs text-slate-500">主线为请求数，红点为失败请求。悬停可查看每个时间段。</p>
      </div>
      <div class="flex items-center gap-3 text-[11px] text-slate-500">
        <span class="inline-flex items-center gap-1.5"><i class="h-2 w-2 rounded-full bg-amber"></i>请求</span>
        <span class="inline-flex items-center gap-1.5"><i class="h-2 w-2 rounded-full bg-signal-red"></i>失败</span>
      </div>
    </div>
    <div v-if="!points.length" class="flex h-[230px] items-center justify-center text-sm text-slate-500">这个时间段还没有调用记录</div>
    <svg v-else :viewBox="`0 0 ${W} ${H}`" class="w-full overflow-visible" role="img" aria-label="请求和错误趋势图">
      <line v-for="n in 4" :key="n" class="chart-grid-line" :x1="PAD.left" :x2="W - PAD.right" :y1="PAD.top + ((H - PAD.top - PAD.bottom) / 4) * n" :y2="PAD.top + ((H - PAD.top - PAD.bottom) / 4) * n" stroke-width="1" />
      <polygon class="chart-area" :points="requestArea" opacity=".65" />
      <polyline class="chart-primary-line" :points="requestLine" fill="none" stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" />
      <g v-for="(point, index) in points" :key="point.start">
        <circle class="chart-primary-point" :cx="point.x" :cy="point.requestY" r="3.5" stroke-width="2">
          <title>{{ point.label }} · {{ point.requests }} 请求 · {{ point.error_requests }} 失败 · {{ formatTokens(point.tokens) }} Token</title>
        </circle>
        <circle v-if="point.error_requests" class="chart-error-point" :cx="point.x" :cy="point.errorY" r="3">
          <title>{{ point.label }} · {{ point.error_requests }} 个失败请求</title>
        </circle>
        <line v-if="index === points.length - 1" class="chart-primary-line" :x1="point.x" :x2="point.x" :y1="PAD.top" :y2="H - PAD.bottom" stroke-dasharray="3 4" opacity=".28" />
      </g>
      <text v-for="point in visibleLabels" :key="`label-${point.start}`" class="chart-axis-label" :x="point.x" :y="H - 10" text-anchor="middle" font-size="10" font-family="ui-monospace, monospace">{{ point.label }}</text>
    </svg>
  </div>
</template>
