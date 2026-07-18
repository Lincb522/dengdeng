<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { UpdateActionResponse, UpdateStatus } from '../../api/types'

const status = ref<UpdateStatus | null>(null)
const loading = ref(true)
const requesting = ref(false)
let pollTimer: number | undefined

const busy = computed(() => requesting.value || status.value?.status === 'queued' || status.value?.status === 'running')
const repositoryURL = computed(() => status.value?.repository?.replace(/\.git$/, '') || '')
const changes = computed(() => Array.isArray(status.value?.changes) ? status.value!.changes : [])
const shortCommit = (value?: string) => !value || value === 'unknown' ? '未记录' : value.slice(0, 12)
const dateTime = (value?: string) => value ? new Date(value).toLocaleString() : '—'

const stages = [
  { id: 'fetching', label: '同步仓库' },
  { id: 'building_frontend', label: '构建前端' },
  { id: 'building_backend', label: '构建服务' },
  { id: 'switching', label: '切换版本' },
  { id: 'health_check', label: '健康检查' },
]
const stageOrder = computed(() => stages.findIndex((item) => item.id === status.value?.stage))

function stageState(index: number) {
  const relevant = status.value?.action === 'rollback' ? [3, 4] : status.value?.action === 'check' ? [0] : [0, 1, 2, 3, 4]
  if (!relevant.includes(index)) return 'skipped'
  const position = relevant.indexOf(index)
  const activePosition = relevant.indexOf(stageOrder.value)
  if (status.value?.status === 'succeeded') return 'done'
  if (status.value?.status === 'failed' && index === stageOrder.value) return 'failed'
  if (activePosition >= 0 && position < activePosition) return 'done'
  if (index === stageOrder.value && busy.value) return 'active'
  return 'pending'
}

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    status.value = await api.get<UpdateStatus>('/api/admin/update/status')
  } finally {
    if (!silent) loading.value = false
  }
  schedulePoll()
}

function schedulePoll() {
  window.clearTimeout(pollTimer)
  pollTimer = window.setTimeout(() => void load(true), busy.value ? 1800 : 15000)
}

async function request(action: 'check' | 'apply' | 'rollback') {
  const prompts = {
    apply: '确认从 main 分支构建并上线新版本？系统会先创建数据库快照，切换时可能有数秒连接重试。',
    rollback: '确认恢复上一运行版本？系统会先创建当前数据库快照，并在回滚后执行健康检查。',
  }
  if (action !== 'check' && !confirm(prompts[action])) return
  requesting.value = true
  try {
    const result = await withToast(
      () => api.post<UpdateActionResponse>(`/api/admin/update/${action}`, {}),
      action === 'check' ? '正在检查仓库' : action === 'apply' ? '更新任务已启动' : '回滚任务已启动',
    )
    if (result) status.value = result.status
  } finally {
    requesting.value = false
    schedulePoll()
  }
}

onMounted(() => void load())
onBeforeUnmount(() => window.clearTimeout(pollTimer))
</script>

<template>
  <div class="update-page">
    <div class="console-page-head">
      <div>
        <h1>版本更新</h1>
        <p class="mt-1 text-sm text-slate-500">服务器直接跟随受信任仓库，构建完成后才切换运行版本。</p>
      </div>
      <button class="btn-ghost" :disabled="loading || busy" @click="load()">刷新状态</button>
    </div>

    <section v-if="loading && !status" class="update-skeleton" aria-label="正在读取更新状态">
      <span></span><span></span><span></span>
    </section>

    <template v-else-if="status">
      <section class="update-release" :class="`is-${status.status}`">
        <div class="update-release-main">
          <div class="update-state-line">
            <span class="update-state-dot" aria-hidden="true"></span>
            <strong>{{ status.message || '等待检查更新' }}</strong>
            <span v-if="status.status === 'queued' || status.status === 'running'" class="update-live">进行中</span>
            <span v-else-if="status.status === 'failed'" class="update-failed">失败</span>
            <span v-else-if="status.update_available" class="update-ready">可更新</span>
            <span v-else-if="status.target_commit" class="update-current">已同步</span>
            <span v-else class="update-pending">待检查</span>
          </div>
          <div class="update-version-row">
            <div>
              <span>当前版本</span>
              <b>{{ status.current_version || '未标记' }}</b>
              <code :title="status.current_commit">{{ shortCommit(status.current_commit) }}</code>
            </div>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h14m-5-5 5 5-5 5" /></svg>
            <div>
              <span>仓库版本</span>
              <b>{{ status.update_available ? '发现新提交' : status.target_commit ? '当前分支' : '尚未检查' }}</b>
              <code :title="status.target_commit">{{ shortCommit(status.target_commit) }}</code>
            </div>
          </div>
        </div>

        <div class="update-actions">
          <button class="btn-ghost" :disabled="busy || !status.enabled" @click="request('check')">检查更新</button>
          <button class="btn-primary" :disabled="busy || !status.enabled || !status.update_available" @click="request('apply')">
            {{ busy && status.action === 'apply' ? '更新中…' : '更新到最新版本' }}
          </button>
          <button class="update-rollback" :disabled="busy || !status.enabled || !status.can_rollback" @click="request('rollback')">恢复上一版本</button>
        </div>
      </section>

      <p v-if="!status.enabled" class="update-disabled">服务器尚未安装更新执行器。按部署文档完成 systemd 与 Polkit 配置后再启用。</p>

      <section class="update-progress" aria-label="更新流程">
        <div class="update-progress-head">
          <div><h2>执行进度</h2><p>构建过程不影响当前服务，只有切换版本时会短暂重启。</p></div>
          <time>{{ dateTime(status.started_at || status.requested_at) }}</time>
        </div>
        <ol>
          <li v-for="(item, index) in stages" :key="item.id" :class="`is-${stageState(index)}`">
            <i aria-hidden="true"><svg v-if="stageState(index) === 'done'" viewBox="0 0 24 24"><path d="m6 12 4 4 8-8" /></svg></i>
            <span>{{ item.label }}</span>
          </li>
        </ol>
      </section>

      <section class="update-changelog" aria-labelledby="update-changelog-title">
        <div class="update-changelog-head">
          <div>
            <h2 id="update-changelog-title">{{ status.update_available ? '待更新内容' : changes.length ? '本次更新' : '更新日志' }}</h2>
            <p>{{ changes.length ? `共 ${changes.length} 个提交，按时间从新到旧排列` : '当前版本与仓库分支一致，没有待更新提交。' }}</p>
          </div>
          <span v-if="changes.length">最多显示 30 条</span>
        </div>
        <ol v-if="changes.length">
          <li v-for="change in changes" :key="change.commit">
            <a :href="`${repositoryURL}/commit/${change.commit}`" target="_blank" rel="noreferrer">{{ change.title }}</a>
            <div><code>{{ shortCommit(change.commit) }}</code><time>{{ dateTime(change.committed_at) }}</time></div>
          </li>
        </ol>
        <div v-else class="update-changelog-empty">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 12 4 4 8-8" /></svg>
          <span>无需更新</span>
        </div>
      </section>

      <section class="update-details">
        <div><span>连接仓库</span><a :href="repositoryURL" target="_blank" rel="noreferrer">{{ status.repository }}</a></div>
        <div><span>跟随分支</span><code>{{ status.branch }}</code></div>
        <div><span>上次完成</span><time>{{ dateTime(status.finished_at) }}</time></div>
        <div><span>回滚版本</span><code :title="status.previous_commit">{{ shortCommit(status.previous_commit) }}</code></div>
      </section>
    </template>
  </div>
</template>
