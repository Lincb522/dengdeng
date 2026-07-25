<script setup lang="ts">
import { computed } from 'vue'
import type { AccountObservedUsage, AccountQuotaSnapshot, AccountQuotaWindow, UpstreamAccount } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'

const props = defineProps<{ account: UpstreamAccount; refreshing?: boolean }>()
const emit = defineEmits<{ refresh: [] }>()
const { trigger, panel, open, panelStyle, clearCloseTimer, show, scheduleClose, togglePinned } = useAnchoredPopover('center')
const tooltipID = `account-quota-${Math.random().toString(36).slice(2)}`

function percent(value?: number) { return Math.min(100, Math.max(0, Number(value) || 0)) }
function number(value?: number) { return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(Number(value) || 0) }
function unit(value?: string) {
	if (value === 'requests') return '次'
	if (value === 'tokens') return 'Tokens'
	if (value === 'quota' || value === 'upstream') return '上游单位'
	return value || ''
}
function windowText(window: AccountQuotaWindow) {
	if (window.used_percent !== undefined && window.used_percent !== null) return `${window.label} · 已用 ${percent(window.used_percent).toFixed(1)}%`
	if (window.remaining !== undefined && window.limit !== undefined) return `${window.label} · 剩余 ${number(window.remaining)} / ${number(window.limit)} ${unit(window.unit)}`
	if (window.remaining !== undefined) return `${window.label} · 剩余 ${number(window.remaining)} ${unit(window.unit)}`
	return window.label
}
function resetText(window: AccountQuotaWindow) {
	if (!window.reset_at) return ''
	const reset = new Date(window.reset_at)
	return Number.isNaN(reset.getTime()) ? '' : reset.toLocaleString()
}
function sourceLabel(snapshot?: AccountQuotaSnapshot) {
	if (!snapshot) return '上游额度'
	if (snapshot.plan_type) return snapshot.plan_type
	return ({ codex_subscription: 'Codex 订阅', claude_subscription: 'Claude 订阅', grok_billing: 'Grok 订阅', api_key_usage: 'API Key 额度', api_key_probe: 'API Key 状态', rate_limit_headers: '上游限额' } as Record<string, string>)[snapshot.source] || `${PLATFORM_LABELS[snapshot.platform] || '上游'} 用量`
}
function state(snapshot?: AccountQuotaSnapshot) {
	if (!snapshot) return { label: '等待同步', cls: 'tag-gray' }
	return ({ ready: { label: '已同步', cls: 'tag-green' }, partial: { label: '部分可用', cls: 'tag-amber' }, error: { label: '刷新失败', cls: 'tag-red' } } as Record<string, { label: string; cls: string }>)[snapshot.state] || { label: '本站记录', cls: 'tag-gray' }
}
function observedText(usage?: AccountObservedUsage) {
	if (!usage) return '暂无本站调用记录'
	return `${usage.label} · ${number(usage.requests)} 次 · ${number(Number(usage.input_tokens || 0) + Number(usage.output_tokens || 0))} Tokens`
}

const quota = computed(() => props.account.quota)
const primaryWindow = computed(() => quota.value?.windows?.[0])
const primaryObserved = computed(() => quota.value?.observed_usage?.find((item) => item.key === '24h') || quota.value?.observed_usage?.[0])
const checkedAt = computed(() => {
	const raw = quota.value?.fetched_at || quota.value?.last_attempt_at
	return raw ? new Date(raw).toLocaleString() : '等待自动刷新'
})
const expiry = computed(() => {
	const raw = quota.value?.subscription_expires_at || props.account.expires_at
	return raw ? new Date(raw).toLocaleString() : '未记录'
})
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="account-quota-trigger"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<span><strong>{{ sourceLabel(quota) }}</strong><small>{{ primaryWindow ? windowText(primaryWindow) : observedText(primaryObserved) }}</small></span>
		<i :class="state(quota).cls">{{ state(quota).label }}</i>
		<svg aria-hidden="true" viewBox="0 0 20 20"><circle cx="10" cy="10" r="7.25" /><path d="M10 8.2v5M10 5.8h.01" /></svg>
	</button>

	<Teleport to="body">
		<Transition name="usage-cost-pop">
			<section v-if="open" :id="tooltipID" ref="panel" class="usage-cost-popover account-quota-popover" :style="panelStyle" role="tooltip" @mouseenter="clearCloseTimer" @mouseleave="scheduleClose">
				<header><strong>额度与用量</strong><span>{{ state(quota).label }}</span></header>
				<div v-if="quota?.windows?.length" class="account-quota-popover-windows">
					<div v-for="window in quota.windows" :key="window.key">
						<div><strong>{{ windowText(window) }}</strong><small v-if="resetText(window)">重置：{{ resetText(window) }}</small></div>
						<span v-if="window.used_percent !== undefined"><i :style="{ width: `${percent(window.used_percent)}%` }"></i></span>
					</div>
				</div>
				<dl class="usage-location-lines account-quota-lines">
					<div><dt>本站用量</dt><dd>{{ observedText(primaryObserved) }}</dd></div>
					<div><dt>套餐到期</dt><dd>{{ expiry }}</dd></div>
					<div><dt>最后同步</dt><dd>{{ checkedAt }}</dd></div>
					<div v-if="quota?.message"><dt>上游说明</dt><dd>{{ quota.message }}</dd></div>
				</dl>
				<button type="button" class="account-quota-refresh" :disabled="refreshing" @click.stop="emit('refresh')">{{ refreshing ? '刷新中…' : '立即刷新额度' }}</button>
			</section>
		</Transition>
	</Teleport>
</template>
