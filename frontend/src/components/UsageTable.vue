<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { UsageLog } from '../api/types'
import { copyText } from '../api/client'
import { useToast } from '../stores/toast'
import UsageCostBreakdown from './UsageCostBreakdown.vue'
import UsageLocationDetail from './UsageLocationDetail.vue'
import UsageModelDetail from './UsageModelDetail.vue'
import UsageTokenDetail from './UsageTokenDetail.vue'

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

function formatLatency(milliseconds: number) {
	if (!milliseconds) return '—'
	if (milliseconds < 1000) return `${milliseconds}ms`
	return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 2 : 1)}s`
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
          <th>模型</th>
          <th>分组</th>
			<th>请求地区</th>
			<th>Token</th>
				<th class="text-right">图片</th>
          <th class="text-right">费用</th>
          <th class="text-right">首字耗时</th>
          <th class="text-right">总耗时</th>
          <th>状态</th>
			<th>请求编号</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="l in items" :key="l.id">
          <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(l.created_at).toLocaleString() }}</td>
          <td v-if="showUser">
            <div class="text-xs text-slate-300">{{ l.user_email || '—' }}</div>
            <div class="mt-0.5 text-[10px] text-slate-500">{{ l.key_name || '未命名密钥' }}</div>
          </td>
          <td><UsageModelDetail :log="l" /></td>
          <td>
            <div class="text-xs text-slate-400">{{ l.group_name || '—' }}</div>
            <div v-if="showUser && l.account_name" class="mt-0.5 text-[10px] text-slate-500">{{ l.account_name }}</div>
          </td>
			<td><UsageLocationDetail :log="l" /></td>
			<td><UsageTokenDetail :log="l" /></td>
				<td class="num text-right text-xs text-signal-cyan">{{ l.image_count || '—' }}</td>
          <td class="num text-right text-xs text-amber"><UsageCostBreakdown :log="l" /></td>
          <td class="num whitespace-nowrap text-right text-xs text-slate-500">{{ formatLatency(l.first_token_ms) }}</td>
          <td class="num whitespace-nowrap text-right text-xs text-slate-500">
				<div>{{ formatLatency(l.duration_ms) }}</div>
				<div v-if="showUser && (l.queue_ms || l.schedule_ms || l.upstream_ms || l.attempt_count)" class="mt-0.5 text-[10px] text-slate-600" :title="`排队 ${l.queue_ms || 0}ms，调度 ${l.schedule_ms || 0}ms，上游 ${l.upstream_ms || 0}ms，尝试 ${l.attempt_count || 0} 次`">
					<span v-if="l.queue_ms">排队 {{ l.queue_ms }}ms · </span>路由 {{ l.schedule_ms || 0 }}ms · 上游 {{ l.upstream_ms || 0 }}ms<span v-if="l.attempt_count > 1"> · {{ l.attempt_count }} 次</span>
				</div>
			</td>
          <td>
            <span :class="l.status_code < 400 ? 'tag-green' : 'tag-red'" :title="l.error_message">{{ l.status_code }}</span>
          </td>
			<td>
				<button v-if="l.request_id" type="button" class="font-mono text-[10px] text-slate-500 underline decoration-dotted underline-offset-2 hover:text-amber" :title="`复制 ${l.request_id}`" @click="copyRequestID(l.request_id)">{{ l.request_id.slice(0, 12) }}</button>
				<span v-else class="text-xs text-slate-500">—</span>
			</td>
        </tr>
        <tr v-if="!items.length">
				<td :colspan="showUser ? 12 : 11" class="py-10 text-center text-sm text-slate-500">暂无记录</td>
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
