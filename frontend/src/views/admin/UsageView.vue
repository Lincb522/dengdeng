<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, getToken, withToast } from '../../api/client'
import { localizedApiError } from '../../api/errors'
import type { Group, UsageLog } from '../../api/types'
import UsageTable from '../../components/UsageTable.vue'
import Pagination from '../../components/Pagination.vue'

const route = useRoute()
const items = ref<UsageLog[]>([])
const groups = ref<Group[]>([])
const total = ref(0)
const page = ref(1)
const size = 30
const loading = ref(false)
const expanded = ref(false)
const filters = ref({
  model: '',
	request_id: '',
  platform: '',
  group_id: '',
  user_id: '',
  account_id: '',
  status: String(route.query.status || ''),
  start: '',
  end: '',
  sort: 'created_at',
  order: 'desc',
})

function params() {
  const query = new URLSearchParams({ page: String(page.value), size: String(size), sort: filters.value.sort, order: filters.value.order })
  for (const [key, value] of Object.entries(filters.value)) {
    if (!value || key === 'sort' || key === 'order') continue
    if (key === 'start' || key === 'end') {
      query.set(key, new Date(value).toISOString())
    } else {
      query.set(key, value)
    }
  }
  return query
}

async function load() {
  loading.value = true
  try {
    const response = await api.get<{ items: UsageLog[]; total: number }>(`/api/admin/usage?${params()}`)
    items.value = response.items || []
    total.value = response.total
  } finally {
    loading.value = false
  }
}

async function downloadCSV() {
  const query = params()
  query.delete('page')
  query.delete('size')
  const result = await withToast(async () => {
		const response = await fetch(`/api/admin/usage/export?${query}`, { headers: { Authorization: `Bearer ${getToken()}` } })
    if (!response.ok) {
			let payload: unknown = null
			try { payload = await response.json() } catch { /* non-JSON response */ }
			throw new Error(localizedApiError(response.status, payload))
    }
    const blob = await response.blob()
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `usage-${new Date().toISOString().slice(0, 10)}.csv`
    link.click()
    URL.revokeObjectURL(url)
  }, '已导出 CSV')
  return result
}

function changePage(nextPage: number) {
  page.value = nextPage
  void load()
}

function search() {
  page.value = 1
  void load()
}

function reset() {
  filters.value = { model: '', request_id: '', platform: '', group_id: '', user_id: '', account_id: '', status: '', start: '', end: '', sort: 'created_at', order: 'desc' }
  search()
}

onMounted(async () => {
  groups.value = await api.get<Group[]>('/api/admin/groups')
  await load()
})
</script>

<template>
  <div>
    <div class="console-page-head usage-page-head">
      <div>
        <h1>全站用量</h1>
        <p>可按时间、用户、路由池或结果查询；导出与列表使用同一组条件。</p>
      </div>
      <div class="flex items-center gap-2">
        <button class="btn-ghost !px-3 !py-1.5 text-xs" @click="expanded = !expanded">{{ expanded ? '收起条件' : '更多条件' }}</button>
        <button class="btn-ghost !px-3 !py-1.5 text-xs" @click="downloadCSV">导出 CSV</button>
      </div>
    </div>

    <section class="usage-filters card mb-4 p-3">
      <div class="usage-filter-grid">
        <input v-model="filters.model" class="input" placeholder="模型名称" @keyup.enter="search" />
        <select v-model="filters.platform" class="input"><option value="">全部平台</option><option value="anthropic">Claude</option><option value="openai">OpenAI</option><option value="gemini">Gemini</option></select>
        <select v-model="filters.group_id" class="input"><option value="">全部分组</option><option v-for="group in groups.filter((g) => !filters.platform || g.platform === filters.platform)" :key="group.id" :value="String(group.id)">{{ group.name }}</option></select>
        <select v-model="filters.status" class="input"><option value="">全部结果</option><option value="success">成功</option><option value="error">失败</option></select>
        <div class="usage-filter-actions"><button class="btn-primary" :disabled="loading" @click="search">{{ loading ? '查询中' : '查询' }}</button><button class="btn-ghost" @click="reset">重置</button></div>
      </div>
      <div v-if="expanded" class="usage-filter-grid usage-filter-grid--advanced">
        <label><span>开始时间</span><input v-model="filters.start" class="input" type="datetime-local" /></label>
        <label><span>结束时间</span><input v-model="filters.end" class="input" type="datetime-local" /></label>
        <label><span>用户 ID</span><input v-model="filters.user_id" class="input" inputmode="numeric" placeholder="例如 42" /></label>
			<label><span>请求编号</span><input v-model="filters.request_id" class="input font-mono" placeholder="ddr_…" /></label>
        <label><span>上游账号 ID</span><input v-model="filters.account_id" class="input" inputmode="numeric" placeholder="例如 8" /></label>
        <label><span>排序</span><select v-model="filters.sort" class="input"><option value="created_at">调用时间</option><option value="cost_micro">费用</option><option value="first_token_ms">首字耗时</option><option value="duration_ms">总耗时</option><option value="status_code">状态码</option></select></label>
        <label><span>顺序</span><select v-model="filters.order" class="input"><option value="desc">从高到低 / 最新</option><option value="asc">从低到高 / 最早</option></select></label>
      </div>
    </section>

    <UsageTable :items="items" show-user />
    <Pagination :page="page" :size="size" :total="total" @change="changePage" />
  </div>
</template>
