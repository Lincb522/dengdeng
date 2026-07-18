<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, downloadFile, withToast } from '../../api/client'
import type { BackupPolicy, BackupRecord } from '../../api/types'
import { useToast } from '../../stores/toast'

const toast = useToast()
const backups = ref<BackupRecord[]>([])
const policy = ref<BackupPolicy>({ enabled: true, interval_hours: 24, retention_days: 30, retention_count: 30 })
const loading = ref(true)
const creating = ref(false)
const saving = ref(false)
const cleaning = ref(false)

const automaticBackups = computed(() => backups.value.filter((record) => record.created_by === 'system:auto'))
const lastAutomatic = computed(() => automaticBackups.value[0])
const nextAutomatic = computed(() => {
  if (!policy.value.enabled) return '已暂停'
  if (!lastAutomatic.value) return '即将创建首份备份'
  const next = new Date(lastAutomatic.value.created_at).getTime() + policy.value.interval_hours * 60 * 60 * 1000
  return next <= Date.now() ? '等待执行' : new Date(next).toLocaleString()
})

function size(value: number) {
  if (!value) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`
}

function creator(record: BackupRecord) {
  return record.created_by === 'system:auto' ? '自动备份' : record.created_by || '系统'
}

async function load() {
  loading.value = true
  try {
    const [records, savedPolicy] = await Promise.all([
      api.get<BackupRecord[]>('/api/admin/backups'),
      api.get<BackupPolicy>('/api/admin/backups/policy'),
    ])
    backups.value = Array.isArray(records) ? records : []
    policy.value = savedPolicy
  } finally { loading.value = false }
}

async function savePolicy() {
  saving.value = true
  try {
    const result = await withToast(
      () => api.put<BackupPolicy>('/api/admin/backups/policy', policy.value),
      policy.value.enabled ? '自动备份策略已启用' : '自动备份已暂停',
    )
    if (result) policy.value = result
  } finally { saving.value = false }
}

async function cleanup() {
  cleaning.value = true
  try {
    const result = await withToast(
      () => api.post<{ deleted: number }>('/api/admin/backups/cleanup', {}),
    )
    if (result) {
      toast.show(`已清理 ${result.deleted} 份自动备份`, 'success')
      await load()
    }
  } finally { cleaning.value = false }
}

async function createBackup() {
  creating.value = true
  try {
    const result = await withToast(() => api.post<BackupRecord>('/api/admin/backups', {}), '手动备份已创建')
    if (result) await load()
  } finally { creating.value = false }
}

async function download(record: BackupRecord) {
  try {
    await downloadFile(`/api/admin/backups/${record.id}/download`, record.filename)
    toast.show('备份下载已开始', 'success')
  } catch (error) { toast.show(error instanceof Error ? error.message : '下载失败', 'error') }
}

async function remove(record: BackupRecord) {
  if (!confirm(`删除数据库备份「${record.filename}」？此操作不可恢复。`)) return
  const done = await withToast(() => api.delete(`/api/admin/backups/${record.id}`), '备份已删除')
  if (done !== null) await load()
}

onMounted(load)
</script>

<template>
  <div class="backup-page">
    <div class="console-page-head">
      <div>
        <h1>数据库备份</h1>
        <p>自动留存 SQLite 一致性快照，手动备份不受清理策略影响。</p>
      </div>
      <div class="backup-head-actions">
        <button class="btn-ghost" :disabled="loading" @click="load">刷新</button>
        <button class="btn-primary" :disabled="creating" @click="createBackup">{{ creating ? '创建中…' : '立即备份' }}</button>
      </div>
    </div>

    <section class="backup-policy" aria-labelledby="backup-policy-title">
      <div class="backup-policy-head">
        <div>
          <div class="backup-policy-title-row">
            <h2 id="backup-policy-title">自动备份</h2>
            <span :class="policy.enabled ? 'is-on' : 'is-off'">{{ policy.enabled ? '运行中' : '已暂停' }}</span>
          </div>
          <p>下一次：{{ nextAutomatic }}</p>
        </div>
        <label class="backup-switch">
          <input v-model="policy.enabled" type="checkbox">
          <span aria-hidden="true"></span>
          <b>{{ policy.enabled ? '启用' : '暂停' }}</b>
        </label>
      </div>

      <div class="backup-policy-fields">
        <label>
          <span>备份间隔</span>
          <div><input v-model.number="policy.interval_hours" type="number" min="1" max="720"><em>小时</em></div>
        </label>
        <label>
          <span>保留时间</span>
          <div><input v-model.number="policy.retention_days" type="number" min="1" max="3650"><em>天</em></div>
        </label>
        <label>
          <span>最多保留</span>
          <div><input v-model.number="policy.retention_count" type="number" min="1" max="365"><em>份</em></div>
        </label>
      </div>

      <div class="backup-policy-foot">
        <p>系统只会清理标记为“自动备份”的过期文件，管理员手动创建的备份不会自动删除。</p>
        <div>
          <button class="btn-ghost" :disabled="cleaning" @click="cleanup">{{ cleaning ? '清理中…' : '立即清理' }}</button>
          <button class="btn-primary" :disabled="saving" @click="savePolicy">{{ saving ? '保存中…' : '保存策略' }}</button>
        </div>
      </div>
    </section>

    <div class="backup-list-head">
      <div><h2>备份记录</h2><p>共 {{ backups.length }} 份，其中 {{ automaticBackups.length }} 份由系统创建</p></div>
      <p>最近自动备份：{{ lastAutomatic ? new Date(lastAutomatic.created_at).toLocaleString() : '暂无' }}</p>
    </div>

    <section class="card overflow-x-auto">
      <table class="table-base min-w-[720px]">
        <thead><tr><th>文件</th><th>来源</th><th>创建时间</th><th>大小</th><th>状态</th><th class="text-right">操作</th></tr></thead>
        <tbody>
          <tr v-for="record in backups" :key="record.id">
            <td class="font-mono text-xs text-slate-300">{{ record.filename }}</td>
            <td><span class="backup-source" :class="record.created_by === 'system:auto' ? 'is-auto' : 'is-manual'">{{ creator(record) }}</span></td>
            <td class="whitespace-nowrap text-xs text-slate-500">{{ new Date(record.created_at).toLocaleString() }}</td>
            <td class="num text-xs text-slate-400">{{ size(record.size_bytes) }}</td>
            <td>
              <span :class="record.status === 'ready' ? 'tag-green' : record.status === 'failed' ? 'tag-red' : 'tag-amber'">{{ record.status === 'ready' ? '可用' : record.status === 'failed' ? '失败' : '创建中' }}</span>
              <p v-if="record.error" class="mt-1 max-w-52 truncate text-xs text-signal-red" :title="record.error">{{ record.error }}</p>
            </td>
            <td class="space-x-2 text-right"><button v-if="record.status === 'ready'" class="btn-ghost !px-2.5 !py-1 text-xs" @click="download(record)">下载</button><button class="btn-danger !px-2.5 !py-1 text-xs" @click="remove(record)">删除</button></td>
          </tr>
          <tr v-if="!backups.length"><td colspan="6" class="py-12 text-center text-sm text-slate-500">还没有备份。启用自动备份后，系统会创建第一份快照。</td></tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<style scoped>
.backup-page { max-width: 76rem; }
.backup-head-actions, .backup-policy-foot > div { display: flex; gap: .55rem; }
.backup-policy { border: 1px solid var(--line); border-radius: .85rem; background: var(--surface); }
.backup-policy-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1.15rem 1.25rem; border-bottom: 1px solid var(--line); }
.backup-policy-title-row { display: flex; align-items: center; gap: .6rem; }
.backup-policy h2, .backup-list-head h2 { color: var(--ink); font-size: .88rem; font-weight: 820; }
.backup-policy-title-row > span { padding: .18rem .48rem; border-radius: 999px; font-size: .62rem; font-weight: 780; }
.backup-policy-title-row > span.is-on { background: rgb(var(--dd-signal-green) / .1); color: rgb(var(--dd-signal-green)); }
.backup-policy-title-row > span.is-off { background: var(--surface-muted); color: var(--ink-soft); }
.backup-policy-head p, .backup-list-head p { margin-top: .25rem; color: var(--ink-soft); font-size: .7rem; }
.backup-switch { display: inline-flex; cursor: pointer; align-items: center; gap: .5rem; color: var(--ink-soft); font-size: .7rem; }
.backup-switch input { position: absolute; width: 1px; height: 1px; opacity: 0; }
.backup-switch span { position: relative; width: 2.35rem; height: 1.32rem; border: 1px solid var(--line); border-radius: 999px; background: var(--surface-muted); transition: background .18s ease, border-color .18s ease; }
.backup-switch span::after { position: absolute; top: .15rem; left: .16rem; width: .9rem; height: .9rem; border-radius: 50%; background: var(--ink-soft); content: ''; transition: transform .18s ease, background .18s ease; }
.backup-switch input:checked + span { border-color: var(--accent); background: var(--accent-soft); }
.backup-switch input:checked + span::after { transform: translateX(1rem); background: var(--accent); }
.backup-switch input:focus-visible + span { outline: 2px solid var(--accent); outline-offset: 3px; }
.backup-policy-fields { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1rem; padding: 1.15rem 1.25rem; }
.backup-policy-fields label > span { display: block; margin-bottom: .42rem; color: var(--ink-soft); font-size: .68rem; font-weight: 720; }
.backup-policy-fields label > div { display: flex; align-items: center; min-height: 2.55rem; border: 1px solid var(--line); border-radius: .62rem; background: var(--surface-muted); }
.backup-policy-fields input { min-width: 0; flex: 1; padding: .6rem .72rem; border: 0; outline: 0; background: transparent; color: var(--ink); font-size: .8rem; font-variant-numeric: tabular-nums; }
.backup-policy-fields em { padding-right: .72rem; color: var(--ink-soft); font-size: .68rem; font-style: normal; }
.backup-policy-fields label > div:focus-within { border-color: var(--accent); box-shadow: 0 0 0 2px rgb(var(--dd-amber) / .12); }
.backup-policy-foot { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .9rem 1.25rem; border-top: 1px solid var(--line); }
.backup-policy-foot p { max-width: 70ch; color: var(--ink-soft); font-size: .68rem; line-height: 1.55; }
.backup-list-head { display: flex; align-items: end; justify-content: space-between; gap: 1rem; margin: 1.55rem 0 .7rem; }
.backup-list-head > p { white-space: nowrap; }
.backup-source { display: inline-flex; padding: .2rem .48rem; border-radius: 999px; font-size: .65rem; font-weight: 740; }
.backup-source.is-auto { background: var(--accent-soft); color: var(--accent); }
.backup-source.is-manual { background: var(--surface-muted); color: var(--ink-soft); }
@media (max-width: 720px) {
  .backup-policy-fields { grid-template-columns: 1fr; }
  .backup-policy-foot, .backup-list-head { align-items: stretch; flex-direction: column; }
  .backup-policy-foot > div { justify-content: flex-end; }
  .backup-list-head > p { white-space: normal; }
}
</style>
