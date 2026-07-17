<script setup lang="ts">
import type { UsageLog } from '../api/types'
import { formatMoney, formatTokens } from '../api/types'
import { copyText } from '../api/client'
import { reasoningLabel } from '../api/reasoning'
import { useToast } from '../stores/toast'

defineProps<{ items: UsageLog[]; showUser?: boolean }>()

const toast = useToast()

async function copyRequestID(id: string) {
	try {
		await copyText(id)
		toast.show('请求编号已复制', 'success')
	} catch (error) {
		toast.show(error instanceof Error ? error.message : '复制失败', 'error')
	}
}
</script>

<template>
  <div class="card overflow-x-auto">
    <table class="table-base">
      <thead>
        <tr>
          <th>时间</th>
          <th v-if="showUser">用户</th>
          <th>模型</th>
          <th>分组</th>
          <th class="text-right">输入</th>
          <th class="text-right">输出</th>
          <th class="text-right">缓存读</th>
				<th class="text-right">缓存创建</th>
				<th class="text-right">图片</th>
          <th class="text-right">费用</th>
          <th class="text-right">耗时</th>
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
          <td>
            <span class="font-mono text-xs text-slate-200">{{ l.model || '-' }}</span>
            <span v-if="l.stream" class="ml-1 text-[10px] text-signal-cyan">SSE</span>
            <div v-if="l.reasoning_effort" class="mt-0.5 text-[10px] text-slate-500">思考强度 Reasoning Effort · {{ reasoningLabel(l.reasoning_effort) }}</div>
          </td>
          <td>
            <div class="text-xs text-slate-400">{{ l.group_name || '—' }}</div>
            <div v-if="showUser && l.account_name" class="mt-0.5 text-[10px] text-slate-500">{{ l.account_name }}</div>
          </td>
          <td class="num text-right text-xs">{{ formatTokens(l.input_tokens) }}</td>
          <td class="num text-right text-xs">{{ formatTokens(l.output_tokens) }}</td>
          <td class="num text-right text-xs text-slate-500">{{ formatTokens(l.cache_read_tokens) }}</td>
				<td class="num text-right text-xs text-slate-500">
					<div>{{ formatTokens(l.cache_write_tokens) }}</div>
					<div v-if="l.cache_write_5m_tokens || l.cache_write_1h_tokens" class="mt-0.5 text-[10px] text-slate-600">5m {{ formatTokens(l.cache_write_5m_tokens) }} · 1h {{ formatTokens(l.cache_write_1h_tokens) }}</div>
				</td>
				<td class="num text-right text-xs text-signal-cyan">{{ l.image_count || '—' }}</td>
          <td class="num text-right text-xs text-amber">{{ formatMoney(l.cost_micro) }}</td>
          <td class="num text-right text-xs text-slate-500">{{ (l.duration_ms / 1000).toFixed(1) }}s</td>
          <td>
            <span :class="l.status_code < 400 ? 'tag-green' : 'tag-red'" :title="l.error_message">{{ l.status_code }}</span>
          </td>
			<td>
				<button v-if="l.request_id" type="button" class="font-mono text-[10px] text-slate-500 underline decoration-dotted underline-offset-2 hover:text-amber" :title="`复制 ${l.request_id}`" @click="copyRequestID(l.request_id)">{{ l.request_id.slice(0, 12) }}</button>
				<span v-else class="text-xs text-slate-500">—</span>
			</td>
        </tr>
        <tr v-if="!items.length">
				<td :colspan="showUser ? 13 : 12" class="py-10 text-center text-sm text-slate-500">暂无记录</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
