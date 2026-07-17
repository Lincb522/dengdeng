<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, downloadFile, withToast } from '../../api/client'
import type { BackupRecord } from '../../api/types'
import { useToast } from '../../stores/toast'

const toast = useToast()
const backups = ref<BackupRecord[]>([])
const loading = ref(true)
const creating = ref(false)

function size(value: number) {
  if (!value) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`
}

async function load() {
  loading.value = true
  try { backups.value = await api.get<BackupRecord[]>('/api/admin/backups') } finally { loading.value = false }
}

async function createBackup() {
  creating.value = true
  try {
    const result = await withToast(() => api.post<BackupRecord>('/api/admin/backups', {}), '数据库快照已创建')
    if (result) await load()
  } finally { creating.value = false }
}

async function download(record: BackupRecord) {
  try {
    await downloadFile(`/api/admin/backups/${record.id}/download`, record.filename)
    toast.show('快照下载已开始', 'success')
  } catch (error) { toast.show(error instanceof Error ? error.message : '下载失败', 'error') }
}

async function remove(record: BackupRecord) {
  if (!confirm(`删除数据库快照「${record.filename}」？此操作不可恢复。`)) return
  const done = await withToast(() => api.delete(`/api/admin/backups/${record.id}`), '快照已删除')
  if (done !== null) await load()
}

onMounted(load)
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>数据库备份</h1>
        <p class="mt-1 text-sm text-slate-500">创建的是 SQLite 一致性快照，保存在服务器受限目录。恢复需要在维护窗口停服完成，不提供网页一键恢复。</p>
      </div>
      <div class="flex gap-2"><button class="btn-ghost" :disabled="loading" @click="load">刷新</button><button class="btn-primary" :disabled="creating" @click="createBackup">{{ creating ? '创建中…' : '创建快照' }}</button></div>
    </div>

    <section class="mb-5 rounded-xl border border-amber/25 bg-amber/5 p-4 text-sm text-slate-300"><strong class="text-amber">安全说明</strong><p class="mt-1 text-xs leading-5 text-slate-400">快照含业务数据与加密后的上游凭据。仅管理员可创建、下载或删除；下载后请按同等敏感级别加密保存，勿通过聊天或公开网盘传输。</p></section>

    <section class="card overflow-x-auto"><table class="table-base min-w-[720px]"><thead><tr><th>文件</th><th>创建者</th><th>创建时间</th><th>大小</th><th>状态</th><th class="text-right">操作</th></tr></thead><tbody><tr v-for="record in backups" :key="record.id"><td class="font-mono text-xs text-slate-300">{{ record.filename }}</td><td class="text-xs text-slate-400">{{ record.created_by || '系统' }}</td><td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(record.created_at).toLocaleString() }}</td><td class="num text-xs text-slate-400">{{ size(record.size_bytes) }}</td><td><span :class="record.status === 'ready' ? 'tag-green' : record.status === 'failed' ? 'tag-red' : 'tag-amber'">{{ record.status === 'ready' ? '可用' : record.status === 'failed' ? '失败' : '创建中' }}</span><p v-if="record.error" class="mt-1 max-w-52 truncate text-xs text-signal-red" :title="record.error">{{ record.error }}</p></td><td class="space-x-2 text-right"><button v-if="record.status === 'ready'" class="btn-ghost !px-2.5 !py-1 text-xs" @click="download(record)">下载</button><button class="btn-danger !px-2.5 !py-1 text-xs" @click="remove(record)">删除</button></td></tr><tr v-if="!backups.length"><td colspan="6" class="py-12 text-center text-sm text-slate-500">还没有数据库快照。</td></tr></tbody></table></section>
  </div>
</template>
