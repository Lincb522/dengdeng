<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import type { UsageLog } from '../api/types'
import { formatMoney } from '../api/types'

const props = defineProps<{ log: UsageLog }>()

const trigger = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const open = ref(false)
const pinned = ref(false)
const panelStyle = ref({ top: '0px', left: '0px' })
const tooltipID = `usage-cost-${Math.random().toString(36).slice(2)}`
let closeTimer: number | undefined

const componentTotal = computed(() =>
	(props.log.input_cost_micro || 0) +
	(props.log.output_cost_micro || 0) +
	(props.log.cache_read_cost_micro || 0) +
	(props.log.cache_write_cost_micro || 0) +
	(props.log.image_cost_micro || 0),
)
const hasSnapshot = computed(() =>
	(props.log.raw_cost_micro || 0) > 0 ||
	componentTotal.value > 0 ||
	(props.log.effective_multiplier || 0) > 0,
)

const cacheWritePrice = computed(() => {
	const prices: string[] = []
	if (props.log.cache_write_5m_tokens > 0) {
		prices.push(`5m ${formatUnitPrice(props.log.cache_write_5m_unit_price || props.log.cache_write_unit_price)}`)
	}
	if (props.log.cache_write_1h_tokens > 0) {
		prices.push(`1h ${formatUnitPrice(props.log.cache_write_1h_unit_price || props.log.cache_write_unit_price)}`)
	}
	if (!prices.length && props.log.cache_write_tokens > 0) {
		prices.push(formatUnitPrice(props.log.cache_write_unit_price))
	}
	return prices.join(' · ')
})

function formatCost(micro: number) {
	if (!micro) return '$0.000000'
	const dollars = micro / 1_000_000
	return `$${dollars.toFixed(dollars < 0.01 ? 6 : 4)}`
}

function formatUnitPrice(price: number) {
	return `$${(price || 0).toFixed(4)} / 1M`
}

function tierLabel(tier?: string) {
	return ({ auto: 'Auto', default: 'Default', flex: 'Flex', priority: 'Priority', scale: 'Scale' } as Record<string, string>)[tier || ''] || tier || '默认'
}

function clearCloseTimer() {
	if (closeTimer !== undefined) {
		window.clearTimeout(closeTimer)
		closeTimer = undefined
	}
}

async function show() {
	clearCloseTimer()
	open.value = true
	await nextTick()
	positionPanel()
}

function scheduleClose() {
	clearCloseTimer()
	if (pinned.value) return
	closeTimer = window.setTimeout(() => {
		open.value = false
	}, 120)
}

function togglePinned() {
	pinned.value = !pinned.value
	if (pinned.value) void show()
	else open.value = false
}

function close() {
	clearCloseTimer()
	pinned.value = false
	open.value = false
}

function positionPanel() {
	const anchor = trigger.value
	const content = panel.value
	if (!anchor || !content) return
	const rect = anchor.getBoundingClientRect()
	const gap = 8
	const margin = 10
	const width = content.offsetWidth
	const height = content.offsetHeight
	const left = Math.min(
		Math.max(margin, rect.right - width),
		Math.max(margin, window.innerWidth - width - margin),
	)
	const below = rect.bottom + gap
	const top = below + height <= window.innerHeight - margin
		? below
		: Math.max(margin, rect.top - height - gap)
	panelStyle.value = { top: `${top}px`, left: `${left}px` }
}

function handleDocumentPointer(event: PointerEvent) {
	const target = event.target as Node
	if (trigger.value?.contains(target) || panel.value?.contains(target)) return
	close()
}

function handleKeydown(event: KeyboardEvent) {
	if (event.key === 'Escape' && open.value) {
		close()
		trigger.value?.focus()
	}
}

function handleViewportChange() {
	if (open.value) positionPanel()
}

onMounted(() => {
	document.addEventListener('pointerdown', handleDocumentPointer)
	document.addEventListener('keydown', handleKeydown)
	window.addEventListener('resize', handleViewportChange)
	window.addEventListener('scroll', handleViewportChange, true)
})

onBeforeUnmount(() => {
	clearCloseTimer()
	document.removeEventListener('pointerdown', handleDocumentPointer)
	document.removeEventListener('keydown', handleKeydown)
	window.removeEventListener('resize', handleViewportChange)
	window.removeEventListener('scroll', handleViewportChange, true)
})
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="usage-cost-trigger"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<span>{{ formatMoney(log.cost_micro) }}</span>
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
				class="usage-cost-popover"
				:style="panelStyle"
				role="tooltip"
				@mouseenter="clearCloseTimer"
				@mouseleave="scheduleClose"
			>
				<header>
					<strong>费用明细</strong>
					<span>USD</span>
				</header>

				<div v-if="hasSnapshot" class="usage-cost-lines">
					<div>
						<span>输入费用<small>{{ formatUnitPrice(log.input_unit_price) }} Token</small></span>
						<b>{{ formatCost(log.input_cost_micro) }}</b>
					</div>
					<div>
						<span>输出费用<small>{{ formatUnitPrice(log.output_unit_price) }} Token</small></span>
						<b>{{ formatCost(log.output_cost_micro) }}</b>
					</div>
					<div v-if="log.cache_read_tokens || log.cache_read_cost_micro">
						<span>缓存读取费用<small>{{ formatUnitPrice(log.cache_read_unit_price) }} Token</small></span>
						<b>{{ formatCost(log.cache_read_cost_micro) }}</b>
					</div>
					<div v-if="log.cache_write_tokens || log.cache_write_cost_micro">
						<span>缓存创建费用<small>{{ cacheWritePrice }} Token</small></span>
						<b>{{ formatCost(log.cache_write_cost_micro) }}</b>
					</div>
					<div v-if="log.image_count || log.image_cost_micro">
						<span>图片费用<small v-if="log.image_unit_price">{{ `$${log.image_unit_price.toFixed(4)} / 张` }}</small></span>
						<b>{{ formatCost(log.image_cost_micro) }}</b>
					</div>
				</div>
				<p v-else class="usage-cost-legacy">这条历史记录只有最终扣费，没有保存费用拆分。</p>

				<dl class="usage-cost-summary">
					<div>
						<dt>服务档位</dt>
						<dd>{{ tierLabel(log.service_tier) }}</dd>
					</div>
					<div v-if="hasSnapshot">
						<dt>综合倍率</dt>
						<dd class="is-rate">{{ (log.effective_multiplier || 0).toFixed(4) }}x</dd>
					</div>
					<div v-if="hasSnapshot">
						<dt>原始费用</dt>
						<dd>{{ formatCost(log.raw_cost_micro) }}</dd>
					</div>
					<div>
						<dt>用户扣费</dt>
						<dd class="is-total">{{ formatCost(log.cost_micro) }}</dd>
					</div>
				</dl>
			</section>
		</Transition>
	</Teleport>
</template>
