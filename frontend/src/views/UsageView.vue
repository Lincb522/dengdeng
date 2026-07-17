<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, downloadFile, withToast } from '../api/client'
import type { UsageLog } from '../api/types'
import UsageTable from '../components/UsageTable.vue'
import Pagination from '../components/Pagination.vue'

const items = ref<UsageLog[]>([])
const total = ref(0)
const page = ref(1)
const size = 20
const modelFilter = ref('')
const requestIDFilter = ref('')

function params(withPagination = true) {
  const q = new URLSearchParams()
  if (withPagination) {
    q.set('page', String(page.value))
    q.set('size', String(size))
  }
  if (modelFilter.value.trim()) q.set('model', modelFilter.value.trim())
  if (requestIDFilter.value.trim()) q.set('request_id', requestIDFilter.value.trim())
  return q
}

async function load() {
  const resp = await api.get<{ items: UsageLog[]; total: number }>(`/api/user/usage?${params()}`)
  items.value = resp.items || []
  total.value = resp.total
}
onMounted(load)

function changePage(p: number) {
  page.value = p
  load()
}

function search() {
  page.value = 1
  load()
}

async function downloadCSV() {
	await withToast(() => downloadFile(`/api/user/usage/export?${params(false)}`, `usage-${new Date().toISOString().slice(0, 10)}.csv`), '已导出 CSV')
}
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>用量明细</h1>
        <p class="mt-1 text-sm text-slate-500">每次调用的 token 与费用记录</p>
      </div>
      <div class="flex flex-wrap gap-2">
        <input v-model="modelFilter" class="input !w-52" placeholder="按模型名筛选" @keyup.enter="search" />
			<input v-model="requestIDFilter" class="input !w-52 font-mono" placeholder="请求编号" @keyup.enter="search" />
        <button class="btn-ghost" @click="search">筛选</button>
			<button class="btn-ghost" @click="downloadCSV">导出 CSV</button>
      </div>
    </div>
    <UsageTable :items="items" />
    <Pagination :page="page" :size="size" :total="total" @change="changePage" />
  </div>
</template>
