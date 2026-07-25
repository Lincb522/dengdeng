<script setup lang="ts">
import { computed } from 'vue'
import type { UsageLog } from '../api/types'
import { useAnchoredPopover } from '../composables/useAnchoredPopover'

const props = defineProps<{ log: UsageLog }>()

const { trigger, panel, open, panelStyle, clearCloseTimer, show, scheduleClose, togglePinned } = useAnchoredPopover('start')
const tooltipID = `usage-user-${Math.random().toString(36).slice(2)}`

const emailParts = computed(() => {
	const email = (props.log.user_email || '').trim()
	const separator = email.lastIndexOf('@')
	if (separator <= 0) return { local: email || `用户 ${props.log.user_id || '—'}`, domain: '' }
	return { local: email.slice(0, separator), domain: email.slice(separator + 1) }
})

const shortName = computed(() => {
	const value = emailParts.value.local
	if (value.length <= 18) return value
	return `${value.slice(0, 10)}…${value.slice(-4)}`
})

const avatarText = computed(() => {
	const source = emailParts.value.local.replace(/[^\p{L}\p{N}]/gu, '')
	if (source) return source.slice(0, 2).toUpperCase()
	return props.log.user_id ? String(props.log.user_id).slice(-2) : 'U'
})
</script>

<template>
	<button
		ref="trigger"
		type="button"
		class="usage-user-trigger"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		:aria-label="`用户详情：${log.user_email || `用户 ${log.user_id}`}`"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<span class="usage-user-avatar" aria-hidden="true">{{ avatarText }}</span>
		<span class="usage-user-summary">
			<strong>{{ shortName }}</strong>
			<small>{{ log.key_name || '未命名密钥' }}</small>
		</span>
		<svg aria-hidden="true" viewBox="0 0 20 20"><circle cx="10" cy="10" r="7.25" /><path d="M10 8.2v5M10 5.8h.01" /></svg>
	</button>

	<Teleport to="body">
		<Transition name="usage-cost-pop">
			<section
				v-if="open"
				:id="tooltipID"
				ref="panel"
				class="usage-cost-popover usage-user-popover"
				:style="panelStyle"
				role="tooltip"
				@mouseenter="clearCloseTimer"
				@mouseleave="scheduleClose"
			>
				<header>
					<strong>用户与密钥</strong>
					<span>USER</span>
				</header>

				<div class="usage-user-profile">
					<span class="usage-user-avatar" aria-hidden="true">{{ avatarText }}</span>
					<div>
						<strong>{{ shortName }}</strong>
						<small v-if="emailParts.domain">@{{ emailParts.domain }}</small>
					</div>
				</div>

				<dl class="usage-location-lines usage-user-lines">
					<div><dt>完整邮箱</dt><dd>{{ log.user_email || '未记录' }}</dd></div>
					<div><dt>用户 ID</dt><dd class="is-code">{{ log.user_id || '—' }}</dd></div>
					<div><dt>密钥名称</dt><dd>{{ log.key_name || '未命名密钥' }}</dd></div>
					<div><dt>密钥 ID</dt><dd class="is-code">{{ log.api_key_id || '—' }}</dd></div>
				</dl>
			</section>
		</Transition>
	</Teleport>
</template>
