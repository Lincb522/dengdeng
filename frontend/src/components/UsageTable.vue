<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { UsageLog } from '../api/types'
import { copyText } from '../api/client'
import { useToast } from '../stores/toast'
import UsageCostBreakdown from './UsageCostBreakdown.vue'
import UsageLocationDetail from './UsageLocationDetail.vue'
import UsageLatencyDetail from './UsageLatencyDetail.vue'
import UsageModelDetail from './UsageModelDetail.vue'
import UsageTokenDetail from './UsageTokenDetail.vue'
import UsageUserDetail from './UsageUserDetail.vue'

const props = defineProps<{ items: UsageLog[]; showUser?: boolean }>()

const toast = useToast()
const tableRegion = ref<HTMLElement | null>(null)
const tableViewport = ref<HTMLElement | null>(null)
const tableElement = ref<HTMLTableElement | null>(null)
const floatingScrollbar = ref<HTMLElement | null>(null)
const scrollWidth = ref(0)
const hasHorizontalOverflow = ref(false)
const regionIsVisible = ref(false)
let resizeObserver: ResizeObserver | null = null
let intersectionObserver: IntersectionObserver | null = null

function updateScrollMetrics() {
	const viewport = tableViewport.value
	const table = tableElement.value
	if (!viewport || !table) return

	scrollWidth.value = Math.max(viewport.scrollWidth, table.scrollWidth)
	hasHorizontalOverflow.value = scrollWidth.value > viewport.clientWidth + 1

	if (floatingScrollbar.value && Math.abs(floatingScrollbar.value.scrollLeft - viewport.scrollLeft) > 1) {
		floatingScrollbar.value.scrollLeft = viewport.scrollLeft
	}
}

function syncHorizontalScroll(source: HTMLElement | null, target: HTMLElement | null) {
	if (!source || !target || Math.abs(source.scrollLeft - target.scrollLeft) <= 1) return
	target.scrollLeft = source.scrollLeft
}

function syncFromTable() {
	syncHorizontalScroll(tableViewport.value, floatingScrollbar.value)
}

function syncFromFloatingScrollbar() {
	syncHorizontalScroll(floatingScrollbar.value, tableViewport.value)
}

onMounted(async () => {
	await nextTick()
	updateScrollMetrics()

	resizeObserver = new ResizeObserver(updateScrollMetrics)
	if (tableViewport.value) resizeObserver.observe(tableViewport.value)
	if (tableElement.value) resizeObserver.observe(tableElement.value)

	intersectionObserver = new IntersectionObserver(([entry]) => {
		regionIsVisible.value = entry?.isIntersecting ?? false
	})
	if (tableRegion.value) intersectionObserver.observe(tableRegion.value)
})

onBeforeUnmount(() => {
	resizeObserver?.disconnect()
	intersectionObserver?.disconnect()
})

watch(
	() => [props.items, props.showUser],
	() => nextTick(updateScrollMetrics),
	{ deep: true },
)

async function copyRequestID(id: string) {
	try {
		await copyText(id)
		toast.show('请求编号已复制', 'success')
	} catch (error) {
		toast.show(error instanceof Error ? error.message : '复制失败', 'error')
	}
}

function billingModeLabel(mode?: string) {
	return ({ usage: '按量', request: '按次', day: '按日', admin: '管理端', none: '未计费' } as Record<string, string>)[mode || ''] || '未记录'
}
</script>

<template>
  <div ref="tableRegion" class="usage-table-region">
    <div ref="tableViewport" class="card usage-table-scroll" @scroll.passive="syncFromTable">
      <table ref="tableElement" v-responsive-table class="table-base">
      <thead>
        <tr>
          <th>时间</th>
          <th v-if="showUser">用户</th>
			<th>分组 / 模型</th>
			<th>请求地区</th>
			<th>Token</th>
				<th class="text-right">图片</th>
          <th class="text-right">费用</th>
			<th>计费模式</th>
			<th>响应耗时</th>
          <th>状态</th>
			<th>请求编号</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="l in items" :key="l.id">
          <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(l.created_at).toLocaleString() }}</td>
			<td v-if="showUser"><UsageUserDetail :log="l" /></td>
			<td><UsageModelDetail :log="l" :show-internal="showUser" /></td>
			<td><UsageLocationDetail :log="l" /></td>
			<td><UsageTokenDetail :log="l" /></td>
				<td class="num text-right text-xs text-signal-cyan">{{ l.image_count || '—' }}</td>
          <td class="num text-right text-xs text-amber"><UsageCostBreakdown :log="l" /></td>
			<td><span class="usage-billing-mode" :class="`is-${l.billing_mode || 'unknown'}`">{{ billingModeLabel(l.billing_mode) }}</span></td>
			<td><UsageLatencyDetail :log="l" /></td>
          <td>
            <span :class="l.status_code < 400 ? 'tag-green' : 'tag-red'" :title="l.error_message">{{ l.status_code }}</span>
          </td>
			<td>
				<button v-if="l.request_id" type="button" class="font-mono text-[10px] text-slate-500 underline decoration-dotted underline-offset-2 hover:text-amber" :title="`复制 ${l.request_id}`" @click="copyRequestID(l.request_id)">{{ l.request_id.slice(0, 12) }}</button>
				<span v-else class="text-xs text-slate-500">—</span>
			</td>
        </tr>
        <tr v-if="!items.length">
				<td :colspan="showUser ? 11 : 10" class="py-10 text-center text-sm text-slate-500">暂无记录</td>
        </tr>
      </tbody>
      </table>
    </div>
    <div
			v-show="hasHorizontalOverflow && regionIsVisible"
			ref="floatingScrollbar"
			class="usage-table-floating-scroll"
			tabindex="0"
			aria-label="用量明细横向滚动"
			@scroll.passive="syncFromFloatingScrollbar"
		>
			<div class="usage-table-floating-scroll-spacer" :style="{ width: `${scrollWidth}px` }" />
		</div>
  </div>
</template>
