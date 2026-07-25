<script setup lang="ts">
import { computed } from 'vue'
import type { UsageLog } from '../api/types'
import { formatTokens } from '../api/types'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'

const props = defineProps<{ log: UsageLog }>()

const { trigger, panel, open, panelStyle, clearCloseTimer, show, scheduleClose, togglePinned } = useAnchoredPopover('start')
const tooltipID = `usage-token-${Math.random().toString(36).slice(2)}`

const cacheWriteTokens = computed(() => props.log.cache_write_tokens || (
	(props.log.cache_write_5m_tokens || 0) + (props.log.cache_write_1h_tokens || 0)
))

const inputIncludesCacheRead = computed(() => ['openai', 'grok', 'gemini'].includes((props.log.platform || '').toLowerCase()))
const regularInputTokens = computed(() => inputIncludesCacheRead.value
	? Math.max(0, props.log.input_tokens - props.log.cache_read_tokens)
	: props.log.input_tokens)
const totalTokens = computed(() => regularInputTokens.value + props.log.output_tokens + props.log.cache_read_tokens + cacheWriteTokens.value)
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="usage-token-trigger"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		:aria-label="`Token 明细：输入 ${regularInputTokens}，输出 ${log.output_tokens}，缓存创建 ${cacheWriteTokens}，缓存读取 ${log.cache_read_tokens}`"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<span class="usage-token-value is-input">
			<svg aria-hidden="true" viewBox="0 0 16 16"><path d="M8 2v10m-3-3 3 3 3-3" /></svg>
			{{ formatTokens(regularInputTokens) }}
		</span>
		<span class="usage-token-value is-output">
			<svg aria-hidden="true" viewBox="0 0 16 16"><path d="M8 14V4M5 7l3-3 3 3" /></svg>
			{{ formatTokens(log.output_tokens) }}
		</span>
		<span class="usage-token-value is-cache-read">
			<svg aria-hidden="true" viewBox="0 0 16 16"><path d="M3 5.5h10l-.8 7H3.8l-.8-7Zm2-2h6l1 2H4l1-2Z" /></svg>
			{{ formatTokens(log.cache_read_tokens) }}
		</span>
		<span class="usage-token-value is-cache-write">
			<svg aria-hidden="true" viewBox="0 0 16 16"><path d="m4 11.5-.5 2 2-.5 7-7-1.5-1.5-7 7Zm6-6 1.5 1.5" /></svg>
			{{ formatTokens(cacheWriteTokens) }}
		</span>
		<svg class="usage-token-info" aria-hidden="true" viewBox="0 0 20 20">
			<circle cx="10" cy="10" r="7.25" />
			<path d="M10 8.2v5M10 5.8h.01" />
		</svg>
	</button>

	<Teleport to="body">
		<Transition name="usage-cost-pop">
			<section
				v-if="open"
				:id="tooltipID"
				ref="panel"
				class="usage-cost-popover usage-token-popover"
				:style="panelStyle"
				role="tooltip"
				@mouseenter="clearCloseTimer"
				@mouseleave="scheduleClose"
			>
				<header>
					<strong>Token 明细</strong>
					<span>TOKEN</span>
				</header>

				<div class="usage-token-lines">
					<div><span>输入 Token</span><b>{{ formatTokens(regularInputTokens) }}</b></div>
					<div><span>输出 Token</span><b>{{ formatTokens(log.output_tokens) }}</b></div>
					<div><span>缓存创建 Token</span><b>{{ formatTokens(cacheWriteTokens) }}</b></div>
					<div v-if="log.cache_write_5m_tokens"><span>创建 5m</span><b>{{ formatTokens(log.cache_write_5m_tokens) }}</b></div>
					<div v-if="log.cache_write_1h_tokens"><span>创建 1h</span><b>{{ formatTokens(log.cache_write_1h_tokens) }}</b></div>
					<div><span>缓存读取 Token</span><b>{{ formatTokens(log.cache_read_tokens) }}</b></div>
				</div>
				<div class="usage-token-total">
					<span>总 Token</span>
					<b>{{ formatTokens(totalTokens) }}</b>
				</div>
				<p v-if="cacheWriteTokens === 0 && log.cache_read_tokens > 0" class="usage-token-note">该上游只返回了缓存读取量，没有提供缓存创建量。</p>
			</section>
		</Transition>
	</Teleport>
</template>
