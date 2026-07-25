<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import type { UsageLog } from '../api/types'

const props = defineProps<{ log: UsageLog }>()

const trigger = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const open = ref(false)
const pinned = ref(false)
const panelStyle = ref({ top: '0px', left: '0px' })
const tooltipID = `usage-location-${Math.random().toString(36).slice(2)}`
let closeTimer: number | undefined

const geoAliases: Record<string, string> = {
	'china': '中国',
	'hong kong': '中国香港',
	'hong kong sar': '中国香港',
	'hong kong sar china': '中国香港',
	'macao': '中国澳门',
	'macau': '中国澳门',
	'taiwan': '中国台湾',
	'united states': '美国',
	'united states of america': '美国',
	'singapore': '新加坡',
	'japan': '日本',
	'south korea': '韩国',
	'republic of korea': '韩国',
	'united kingdom': '英国',
	'germany': '德国',
	'france': '法国',
	'canada': '加拿大',
	'australia': '澳大利亚',
	'russia': '俄罗斯',
	'india': '印度',
	'indonesia': '印度尼西亚',
	'malaysia': '马来西亚',
	'thailand': '泰国',
	'vietnam': '越南',
	'philippines': '菲律宾',
	'central and western': '中西区',
	'central and western district': '中西区',
	'wan chai': '湾仔',
	'kowloon': '九龙',
	'kowloon city': '九龙城',
	'new territories': '新界',
	'central': '中环',
	'beijing': '北京',
	'shanghai': '上海',
	'guangdong': '广东',
	'guangzhou': '广州',
	'shenzhen': '深圳',
	'tokyo': '东京',
	'osaka': '大阪',
	'california': '加利福尼亚',
	'los angeles': '洛杉矶',
	'new york': '纽约',
	'local network': '局域网',
}

function localizeGeoName(value?: string) {
	const normalized = (value || '').trim()
	if (!normalized) return ''
	return geoAliases[normalized.toLowerCase()] || normalized
}

function uniqueParts(parts: string[]) {
	return parts.filter((part, index) => part && parts.indexOf(part) === index)
}

function localizedLocation(value?: string) {
	return uniqueParts((value || '')
		.split(/\s*[·,/]\s*/)
		.map(localizeGeoName))
		.join(' · ')
}

const locationParts = computed(() => uniqueParts([
	localizeGeoName(props.log.ip_country),
	localizeGeoName(props.log.ip_region),
	localizeGeoName(props.log.ip_city),
]))

const fullLocation = computed(() => {
	if (locationParts.value.length) return locationParts.value.join(' · ')
	return localizedLocation(props.log.ip_location)
})

const shortLocation = computed(() => {
	if (!props.log.client_ip) return '未记录'
	if (/局域网|local network/i.test(props.log.ip_location || '')) return '局域网'
	const parts = locationParts.value.length
		? locationParts.value
		: uniqueParts(localizedLocation(props.log.ip_location).split(' · ').filter(Boolean))
	if (!parts.length) return '地区解析中'
	const chineseParts = parts.filter((part) => /[\u3400-\u9fff]/.test(part))
	return chineseParts.length ? chineseParts.slice(-2).join(' · ') : '海外地区'
})

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
		Math.max(margin, rect.left),
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
		class="usage-location-trigger"
		:aria-expanded="open"
		:aria-describedby="open ? tooltipID : undefined"
		@click.stop="togglePinned"
		@mouseenter="show"
		@mouseleave="scheduleClose"
		@focus="show"
		@blur="scheduleClose"
	>
		<span>{{ shortLocation }}</span>
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
				class="usage-cost-popover usage-location-popover"
				:style="panelStyle"
				role="tooltip"
				@mouseenter="clearCloseTimer"
				@mouseleave="scheduleClose"
			>
				<header>
					<strong>请求位置</strong>
					<span>IP</span>
				</header>

				<dl class="usage-location-lines">
					<div>
						<dt>IP 地址</dt>
						<dd class="is-code">{{ log.client_ip || '未记录' }}</dd>
					</div>
					<div>
						<dt>完整地区</dt>
						<dd>{{ fullLocation || (log.client_ip ? '解析中' : '未记录') }}</dd>
					</div>
					<div v-if="log.ip_country">
						<dt>国家 / 地区</dt>
						<dd>{{ localizeGeoName(log.ip_country) }}</dd>
					</div>
					<div v-if="log.ip_region">
						<dt>省 / 州</dt>
						<dd>{{ localizeGeoName(log.ip_region) }}</dd>
					</div>
					<div v-if="log.ip_city">
						<dt>城市</dt>
						<dd>{{ localizeGeoName(log.ip_city) }}</dd>
					</div>
					<div v-if="log.ip_isp">
						<dt>运营商</dt>
						<dd>{{ log.ip_isp }}</dd>
					</div>
				</dl>
			</section>
		</Transition>
	</Teleport>
</template>
