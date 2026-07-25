<script setup lang="ts">
import { computed } from 'vue'
import type { OpsSystemMetric, OpsSystemMetrics } from '../api/types'

const props = defineProps<{ system: OpsSystemMetrics; history: OpsSystemMetric[] }>()

const CHART_WIDTH = 760
const CHART_HEIGHT = 176
const CHART_PAD = { top: 14, right: 12, bottom: 18, left: 12 }

const rows = computed(() => {
	const source = Array.isArray(props.history) ? props.history : []
	if (source.length <= 160) return source
	const step = Math.ceil(source.length / 160)
	return source.filter((_, index) => index % step === 0 || index === source.length - 1)
})

const latest = computed(() => props.system)
const networkMax = computed(() => Math.max(1, ...rows.value.flatMap((row) => [row.network_rx_bytes_per_sec || 0, row.network_tx_bytes_per_sec || 0])))
const cpuPeak = computed(() => Math.max(latest.value.cpu_percent || 0, ...rows.value.map((row) => row.cpu_percent || 0)))
const memoryPeak = computed(() => Math.max(latest.value.memory_percent || 0, ...rows.value.map((row) => row.memory_percent || 0)))
const firstLabel = computed(() => rows.value.length ? new Date(rows.value[0].bucket_at).toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—')
const lastLabel = computed(() => rows.value.length ? new Date(rows.value.at(-1)!.bucket_at).toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—')
const sampledAt = computed(() => latest.value.sampled_at ? new Date(latest.value.sampled_at).toLocaleString() : lastLabel.value)

const state = computed(() => {
	if (!latest.value.db_ok || latest.value.cpu_percent >= 95 || latest.value.memory_percent >= 97 || latest.value.disk_percent >= 97) return { label: '需要处理', className: 'is-critical' }
	if (latest.value.cpu_percent >= 80 || latest.value.memory_percent >= 85 || latest.value.disk_percent >= 85) return { label: '负载偏高', className: 'is-warning' }
	return { label: '运行正常', className: 'is-healthy' }
})

function clampPercent(value: number) {
	return Math.min(100, Math.max(0, Number(value) || 0))
}

function percentagePath(field: 'cpu_percent' | 'memory_percent') {
	return linePath(rows.value.map((row) => clampPercent(row[field])), 100)
}

function networkPath(field: 'network_rx_bytes_per_sec' | 'network_tx_bytes_per_sec') {
	return linePath(rows.value.map((row) => Math.max(0, row[field] || 0)), networkMax.value)
}

function linePath(values: number[], maximum: number) {
	if (!values.length) return ''
	const width = CHART_WIDTH - CHART_PAD.left - CHART_PAD.right
	const height = CHART_HEIGHT - CHART_PAD.top - CHART_PAD.bottom
	return values.map((value, index) => {
		const x = CHART_PAD.left + (values.length === 1 ? width : index / (values.length - 1) * width)
		const y = CHART_PAD.top + height - Math.min(value / Math.max(maximum, 1), 1) * height
		return `${index ? 'L' : 'M'}${x.toFixed(2)} ${y.toFixed(2)}`
	}).join(' ')
}

function formatBytes(value: number, rate = false) {
	const safe = Math.max(0, Number(value) || 0)
	const units = ['B', 'KB', 'MB', 'GB', 'TB']
	const index = safe ? Math.min(Math.floor(Math.log(safe) / Math.log(1024)), units.length - 1) : 0
	const amount = safe / 1024 ** index
	return `${amount.toFixed(index > 1 ? 1 : amount >= 10 ? 0 : 1)} ${units[index]}${rate ? '/s' : ''}`
}

function formatPercent(value: number) {
	return `${clampPercent(value).toFixed(1)}%`
}

function formatUptime(seconds: number) {
	let remaining = Math.max(0, Math.floor(Number(seconds) || 0))
	const days = Math.floor(remaining / 86400)
	remaining %= 86400
	const hours = Math.floor(remaining / 3600)
	const minutes = Math.floor((remaining % 3600) / 60)
	if (days) return `${days} 天 ${hours} 小时`
	if (hours) return `${hours} 小时 ${minutes} 分钟`
	return `${minutes} 分钟`
}
</script>

<template>
	<section class="server-monitor card" aria-labelledby="server-monitor-title">
		<header class="server-monitor-head">
			<div>
				<div class="server-monitor-title-row">
					<h3 id="server-monitor-title">服务器监控</h3>
					<span class="server-monitor-state" :class="state.className"><i></i>{{ state.label }}</span>
				</div>
				<p><strong>{{ latest.hostname || '当前节点' }}</strong><span>{{ latest.os || 'linux' }} / {{ latest.arch || '—' }}</span><span>{{ latest.cpu_cores || '—' }} 核</span></p>
			</div>
			<div class="server-monitor-sampled"><span>最近采样</span><strong>{{ sampledAt }}</strong></div>
		</header>

		<div class="server-monitor-body">
			<div class="server-resource-list">
				<article>
					<div class="server-resource-head"><span>CPU</span><strong>{{ formatPercent(latest.cpu_percent) }}</strong></div>
					<div class="server-resource-track" role="progressbar" aria-label="CPU 使用率" :aria-valuenow="clampPercent(latest.cpu_percent)" aria-valuemin="0" aria-valuemax="100"><i :style="{ width: `${clampPercent(latest.cpu_percent)}%` }"></i></div>
					<small>负载 {{ (latest.load_1 || 0).toFixed(2) }} / {{ (latest.load_5 || 0).toFixed(2) }} / {{ (latest.load_15 || 0).toFixed(2) }} · 进程 {{ formatPercent(latest.process_cpu_percent) }}</small>
				</article>
				<article>
					<div class="server-resource-head"><span>内存</span><strong>{{ formatPercent(latest.memory_percent) }}</strong></div>
					<div class="server-resource-track" role="progressbar" aria-label="内存使用率" :aria-valuenow="clampPercent(latest.memory_percent)" aria-valuemin="0" aria-valuemax="100"><i :style="{ width: `${clampPercent(latest.memory_percent)}%` }"></i></div>
					<small>{{ formatBytes(latest.memory_used_bytes) }} / {{ formatBytes(latest.memory_total_bytes) }} · 服务 RSS {{ formatBytes(latest.process_rss_bytes) }}</small>
				</article>
				<article>
					<div class="server-resource-head"><span>磁盘</span><strong>{{ formatPercent(latest.disk_percent) }}</strong></div>
					<div class="server-resource-track" role="progressbar" aria-label="磁盘使用率" :aria-valuenow="clampPercent(latest.disk_percent)" aria-valuemin="0" aria-valuemax="100"><i :style="{ width: `${clampPercent(latest.disk_percent)}%` }"></i></div>
					<small>{{ formatBytes(latest.disk_used_bytes) }} / {{ formatBytes(latest.disk_total_bytes) }} · 可用 {{ formatBytes(Math.max(0, latest.disk_total_bytes - latest.disk_used_bytes)) }}</small>
				</article>
				<article class="server-network-row">
					<div><span>网络接收</span><strong>↓ {{ formatBytes(latest.network_rx_bytes_per_sec, true) }}</strong></div>
					<div><span>网络发送</span><strong>↑ {{ formatBytes(latest.network_tx_bytes_per_sec, true) }}</strong></div>
				</article>
			</div>

			<div class="server-chart-stack">
				<article class="server-trend">
					<header><div><strong>资源趋势</strong><span>{{ firstLabel }} — {{ lastLabel }}</span></div><div class="server-trend-legend"><span><i class="is-cpu"></i>CPU</span><span><i class="is-memory"></i>内存</span></div></header>
				<div v-if="rows.length" class="server-trend-canvas">
					<svg :viewBox="`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`" role="img" aria-label="服务器 CPU 与内存使用率趋势">
						<line v-for="value in [25, 50, 75]" :key="value" class="server-chart-grid" :x1="CHART_PAD.left" :x2="CHART_WIDTH - CHART_PAD.right" :y1="CHART_PAD.top + (100 - value) / 100 * (CHART_HEIGHT - CHART_PAD.top - CHART_PAD.bottom)" :y2="CHART_PAD.top + (100 - value) / 100 * (CHART_HEIGHT - CHART_PAD.top - CHART_PAD.bottom)" />
						<path class="server-chart-line is-cpu" :d="percentagePath('cpu_percent')" />
						<path class="server-chart-line is-memory" :d="percentagePath('memory_percent')" />
					</svg>
				</div>
				<div v-else class="server-chart-empty">等待首个分钟采样</div>
				<footer><span>CPU 峰值 <strong>{{ formatPercent(cpuPeak) }}</strong></span><span>内存峰值 <strong>{{ formatPercent(memoryPeak) }}</strong></span></footer>
				</article>

				<article class="server-trend server-trend-network">
					<header><div><strong>网络吞吐</strong><span>排除本机回环流量</span></div><div class="server-trend-legend"><span><i class="is-rx"></i>接收</span><span><i class="is-tx"></i>发送</span></div></header>
					<div v-if="rows.length" class="server-trend-canvas">
						<svg :viewBox="`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`" role="img" aria-label="服务器网络接收与发送吞吐趋势">
						<line v-for="value in [25, 50, 75]" :key="value" class="server-chart-grid" :x1="CHART_PAD.left" :x2="CHART_WIDTH - CHART_PAD.right" :y1="CHART_PAD.top + (100 - value) / 100 * (CHART_HEIGHT - CHART_PAD.top - CHART_PAD.bottom)" :y2="CHART_PAD.top + (100 - value) / 100 * (CHART_HEIGHT - CHART_PAD.top - CHART_PAD.bottom)" />
						<path class="server-chart-line is-rx" :d="networkPath('network_rx_bytes_per_sec')" />
						<path class="server-chart-line is-tx" :d="networkPath('network_tx_bytes_per_sec')" />
					</svg>
				</div>
				<div v-else class="server-chart-empty">等待首个分钟采样</div>
				<footer><span>当前接收 <strong>{{ formatBytes(latest.network_rx_bytes_per_sec, true) }}</strong></span><span>当前发送 <strong>{{ formatBytes(latest.network_tx_bytes_per_sec, true) }}</strong></span></footer>
				</article>
			</div>
		</div>

		<footer class="server-runtime-strip">
			<div><span>主机运行</span><strong>{{ formatUptime(latest.host_uptime_seconds) }}</strong></div>
			<div><span>服务运行</span><strong>{{ formatUptime(latest.uptime_seconds) }}</strong></div>
			<div><span>Goroutine</span><strong>{{ latest.goroutines || 0 }}</strong></div>
			<div><span>数据库</span><strong :class="latest.db_ok ? 'text-signal-green' : 'text-signal-red'">{{ latest.db_ok ? '正常' : '异常' }}</strong></div>
			<div><span>连接池</span><strong>{{ latest.db_in_use || 0 }} / {{ latest.db_open_connections || 0 }}</strong></div>
		</footer>
	</section>
</template>
