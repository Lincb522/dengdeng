<script setup lang="ts">
import { computed } from 'vue'
import type { UsageLog } from '../api/types'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'

const props = defineProps<{ log: UsageLog }>()

const { trigger, panel, open, panelStyle, clearCloseTimer, show, scheduleClose, togglePinned } = useAnchoredPopover('end')
const tooltipID = `usage-latency-${Math.random().toString(36).slice(2)}`

function formatLatency(milliseconds: number) {
	if (!milliseconds) return '—'
	if (milliseconds < 1000) return `${milliseconds}ms`
	const seconds = milliseconds / 1000
	const formatted = seconds.toFixed(seconds < 60 ? 2 : 1).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
	return `${formatted}s`
}

function latencyLevel(milliseconds: number, kind: 'first' | 'total') {
	if (!milliseconds) return 'none'
	const fast = kind === 'first' ? 1_500 : 15_000
	const medium = kind === 'first' ? 5_000 : 45_000
	if (milliseconds <= fast) return 'fast'
	if (milliseconds <= medium) return 'medium'
	return 'slow'
}

const totalLevel = computed(() => latencyLevel(props.log.duration_ms, 'total'))
const speedLabel = computed(() => ({ fast: '快速', medium: '一般', slow: '较慢', none: '未记录' }[totalLevel.value]))
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="usage-latency-trigger"
		:class="`is-${totalLevel}`"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		:aria-label="`响应耗时：首字 ${formatLatency(log.first_token_ms)}，总耗时 ${formatLatency(log.duration_ms)}`"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<i class="usage-latency-indicator" aria-hidden="true"></i>
		<span>首字</span>
		<b :class="`is-${latencyLevel(log.first_token_ms, 'first')}`">{{ formatLatency(log.first_token_ms) }}</b>
		<span>总耗时</span>
		<b :class="`is-${totalLevel}`">{{ formatLatency(log.duration_ms) }}</b>
		<svg aria-hidden="true" viewBox="0 0 20 20"><circle cx="10" cy="10" r="7.25" /><path d="M10 8.2v5M10 5.8h.01" /></svg>
	</button>

	<Teleport to="body">
		<Transition name="usage-cost-pop">
			<section
				v-if="open"
				:id="tooltipID"
				ref="panel"
				class="usage-cost-popover usage-latency-popover"
				:style="panelStyle"
				role="tooltip"
				@mouseenter="clearCloseTimer"
				@mouseleave="scheduleClose"
			>
				<header>
					<strong>响应耗时</strong>
					<span :class="`is-${totalLevel}`">{{ speedLabel }}</span>
				</header>

				<dl class="usage-location-lines usage-latency-lines">
					<div><dt>首字耗时</dt><dd :class="`is-${latencyLevel(log.first_token_ms, 'first')}`">{{ formatLatency(log.first_token_ms) }}</dd></div>
					<div><dt>排队等待</dt><dd>{{ formatLatency(log.queue_ms) }}</dd></div>
					<div><dt>内部路由</dt><dd>{{ formatLatency(log.schedule_ms) }}</dd></div>
					<div><dt>上游处理</dt><dd>{{ formatLatency(log.upstream_ms) }}</dd></div>
					<div><dt>总耗时</dt><dd :class="`is-${totalLevel}`">{{ formatLatency(log.duration_ms) }}</dd></div>
					<div><dt>上游尝试</dt><dd>{{ log.attempt_count || 1 }} 次</dd></div>
				</dl>
			</section>
		</Transition>
	</Teleport>
</template>
