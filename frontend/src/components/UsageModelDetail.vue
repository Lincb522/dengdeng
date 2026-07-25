<script setup lang="ts">
import { computed } from 'vue'
import type { UsageLog } from '../api/types'
import { reasoningLabel } from '../api/reasoning'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'

const props = defineProps<{ log: UsageLog; showInternal?: boolean }>()

const { trigger, panel, open, panelStyle, clearCloseTimer, show, scheduleClose, togglePinned } = useAnchoredPopover('start')
const tooltipID = `usage-model-${Math.random().toString(36).slice(2)}`

const wordLabels: Record<string, string> = {
	claude: 'Claude',
	opus: 'Opus',
	sonnet: 'Sonnet',
	haiku: 'Haiku',
	mythos: 'Mythos',
	fable: 'Fable',
	gpt: 'GPT',
	gemini: 'Gemini',
	codex: 'Codex',
	image: 'Image',
	flash: 'Flash',
	lite: 'Lite',
	pro: 'Pro',
	mini: 'Mini',
	nano: 'Nano',
	sol: 'Sol',
	terra: 'Terra',
	luna: 'Luna',
}

function modelShortName(model?: string) {
	const normalized = (model || '').trim().replace(/^models\//, '')
	if (!normalized) return '未记录'
	const withoutSnapshot = normalized.replace(/-\d{8}$/, '')
	const parts = withoutSnapshot.split('-').filter(Boolean)
	const output: string[] = []
	for (let index = 0; index < parts.length; index += 1) {
		const current = parts[index]
		const next = parts[index + 1]
		if (/^\d+$/.test(current) && /^\d+$/.test(next || '') && Number(current) <= 10) {
			output.push(`${current}.${next}`)
			index += 1
			continue
		}
		output.push(wordLabels[current.toLowerCase()] || current)
	}
	return output.join(' ')
}

function providerLabel(model?: string, platform?: string) {
	const normalizedPlatform = (platform || '').toLowerCase()
	if (normalizedPlatform === 'anthropic') return 'Anthropic'
	if (normalizedPlatform === 'gemini') return 'Google'
	if (normalizedPlatform === 'openai') return 'OpenAI'
	if (normalizedPlatform === 'grok') return 'xAI'
	const normalized = (model || '').toLowerCase()
	if (normalized.startsWith('claude-')) return 'Anthropic'
	if (normalized.startsWith('gemini-')) return 'Google'
	if (/^(gpt-|o\d|codex-)/.test(normalized)) return 'OpenAI'
	return '自动识别'
}

function protocolLabel(path?: string) {
	const normalized = (path || '').toLowerCase()
	if (normalized.includes('/messages')) return 'Anthropic Messages'
	if (normalized.includes('/responses')) return 'OpenAI Responses'
	if (normalized.includes('/chat/completions')) return 'OpenAI Chat Completions'
	if (normalized.includes('generatecontent')) return 'Gemini GenerateContent'
	return path || '未记录'
}

const shortName = computed(() => modelShortName(props.log.model))
const groupName = computed(() => (props.log.group_name || '').trim() || '未记录分组')
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="usage-model-trigger"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		:aria-label="`路由与模型详情：${groupName}，${log.model || '未记录模型'}`"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<span class="usage-model-summary">
			<strong>{{ groupName }}</strong>
			<small>{{ log.model || '未记录模型' }}</small>
		</span>
		<svg aria-hidden="true" viewBox="0 0 20 20">
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
				class="usage-cost-popover usage-model-popover"
				:style="panelStyle"
				role="tooltip"
				@mouseenter="clearCloseTimer"
				@mouseleave="scheduleClose"
			>
				<header>
					<strong>路由与模型</strong>
					<span>{{ providerLabel(log.model, log.platform) }}</span>
				</header>

				<dl class="usage-location-lines usage-model-lines">
					<div>
						<dt>分组</dt>
						<dd>{{ groupName }}</dd>
					</div>
					<div>
						<dt>模型简称</dt>
						<dd>{{ shortName }}</dd>
					</div>
					<div>
						<dt>完整模型 ID</dt>
						<dd class="is-code">{{ log.model || '未记录' }}</dd>
					</div>
					<div>
						<dt>平台</dt>
						<dd>{{ providerLabel(log.model, log.platform) }}</dd>
					</div>
					<div>
						<dt>思考强度</dt>
						<dd>{{ log.reasoning_effort ? reasoningLabel(log.reasoning_effort) : '默认' }}</dd>
					</div>
					<div>
						<dt>流式输出</dt>
						<dd>{{ log.stream ? '是 · SSE' : '否' }}</dd>
					</div>
					<div>
						<dt>入站端点</dt>
						<dd class="is-code">{{ log.request_path || '未记录' }}</dd>
					</div>
					<div>
						<dt>接口协议</dt>
						<dd>{{ protocolLabel(log.request_path) }}</dd>
					</div>
					<div v-if="showInternal">
						<dt>上游账号</dt>
						<dd>{{ log.account_name || '未记录' }}</dd>
					</div>
					<div v-if="showInternal">
						<dt>路由编号</dt>
						<dd class="is-code">分组 {{ log.group_id || '—' }} · 账号 {{ log.account_id || '—' }}</dd>
					</div>
				</dl>
			</section>
		</Transition>
	</Teleport>
</template>
