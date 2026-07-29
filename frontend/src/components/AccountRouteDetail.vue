<script setup lang="ts">
import { computed } from 'vue'
import type { UpstreamAccount } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'
import ProviderLogo from './ProviderLogo.vue'

const props = defineProps<{ account: UpstreamAccount }>()
const { trigger, panel, open, panelStyle, clearCloseTimer, show, scheduleClose, togglePinned } = useAnchoredPopover('start')
const tooltipID = `account-route-${Math.random().toString(36).slice(2)}`

function compact(value: string, limit = 22) {
	const normalized = value.trim()
	if (normalized.length <= limit) return normalized
	return `${normalized.slice(0, limit - 7)}…${normalized.slice(-6)}`
}

const accountGroups = computed(() => {
	const values = props.account.groups?.length ? props.account.groups : (props.account.group ? [props.account.group] : [])
	return values.filter((group, index) => values.findIndex((candidate) => candidate.id === group.id) === index)
})
const groupName = computed(() => accountGroups.value.map((group) => group.name).join('、') || '未分组')
const platformName = computed(() => PLATFORM_LABELS[props.account.platform] || props.account.platform || '未知平台')
const authName = computed(() => ({ api_key: 'API Key', oauth: 'OAuth', agent_identity: 'Agent Identity' } as Record<string, string>)[props.account.auth_type] || props.account.auth_type)
const summaryName = computed(() => compact(props.account.name || `账号 ${props.account.id}`, 24))
const summaryRoute = computed(() => `${compact(groupName.value, 18)} · ${platformName.value}`)
const baseURL = computed(() => props.account.base_url || '官方默认地址')
const proxyName = computed(() => props.account.proxy?.name || '默认出口')
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="account-route-trigger"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		:aria-label="`账号路由详情：${account.name}`"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<ProviderLogo :platform="account.platform" size="md" />
		<span class="account-route-summary"><strong>{{ summaryName }}</strong><small>{{ summaryRoute }}</small></span>
		<svg aria-hidden="true" viewBox="0 0 20 20"><circle cx="10" cy="10" r="7.25" /><path d="M10 8.2v5M10 5.8h.01" /></svg>
	</button>

	<Teleport to="body">
		<Transition name="usage-cost-pop">
			<section
				v-if="open"
				:id="tooltipID"
				ref="panel"
				class="usage-cost-popover account-route-popover"
				:style="panelStyle"
				role="tooltip"
				@mouseenter="clearCloseTimer"
				@mouseleave="scheduleClose"
			>
				<header><strong>账号与路由</strong><span class="provider-inline-label"><ProviderLogo :platform="account.platform" size="sm" />{{ platformName }}</span></header>
				<dl class="usage-location-lines account-route-lines">
					<div><dt>账号名称</dt><dd>{{ account.name || '未记录' }}</dd></div>
					<div><dt>账号邮箱</dt><dd>{{ account.email || '未记录' }}</dd></div>
					<div><dt>可用分组</dt><dd>{{ groupName }}</dd></div>
					<div><dt>平台</dt><dd>{{ platformName }}</dd></div>
					<div><dt>凭据类型</dt><dd>{{ authName }}</dd></div>
					<div><dt>Base URL</dt><dd class="is-code">{{ baseURL }}</dd></div>
					<div><dt>代理出口</dt><dd>{{ proxyName }}</dd></div>
					<div><dt>调度参数</dt><dd>优先级 {{ account.priority }} · 并发 {{ account.concurrency > 0 ? account.concurrency : '不限' }}</dd></div>
					<div><dt>上游标识</dt><dd class="is-code">{{ account.account_id || '未记录' }}</dd></div>
				</dl>
			</section>
		</Transition>
	</Teleport>
</template>
