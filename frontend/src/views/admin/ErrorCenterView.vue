<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, withToast } from '../../api/client'
import type { ErrorCenterScopeSummary, ErrorCenterSummary, OpsErrorLog, SiteErrorLog } from '../../api/types'
import { PLATFORM_LABELS } from '../../api/types'
import AppModal from '../../components/AppModal.vue'
import Pagination from '../../components/Pagination.vue'

type Scope = 'site' | 'api'
type ErrorItem = SiteErrorLog | OpsErrorLog

const scope = ref<Scope>('site')
const range = ref('24h')
const summary = ref<ErrorCenterSummary | null>(null)
const siteItems = ref<SiteErrorLog[]>([])
const apiItems = ref<OpsErrorLog[]>([])
const total = ref(0)
const page = ref(1)
const size = ref(30)
const loading = ref(false)
const selected = ref<ErrorItem | null>(null)
const status = ref<'open' | 'resolved' | ''>('open')
const category = ref('')
const level = ref('')
const platform = ref('')
const errorType = ref('')
const keyword = ref('')

const items = computed<ErrorItem[]>(() => scope.value === 'site' ? siteItems.value : apiItems.value)
const scopeSummary = computed<ErrorCenterScopeSummary>(() => summary.value?.[scope.value] || {
  total: 0, open: 0, resolved: 0, critical: 0, last_hour: 0, retryable: 0, business_limited: 0, categories: [],
})
const maxCategoryCount = computed(() => Math.max(...scopeSummary.value.categories.map((item) => item.count), 1))
const unresolvedRate = computed(() => scopeSummary.value.total ? scopeSummary.value.open / scopeSummary.value.total * 100 : 0)
const selectedSite = computed(() => scope.value === 'site' ? selected.value as SiteErrorLog | null : null)
const selectedAPI = computed(() => scope.value === 'api' ? selected.value as OpsErrorLog | null : null)

const categoryLabels: Record<string, string> = {
  frontend: '前端运行',
  authentication: '认证与鉴权',
  administration: '管理端',
  user_console: '用户控制台',
  payment: '支付',
  referral: '推广',
  public_site: '公开页面',
  invalid_request: '请求参数',
  rate_limit: '限流',
  no_available_account: '无可用账号',
  upstream_error: '上游响应',
  upstream: '上游响应',
}

function categoryLabel(value: string) {
  return categoryLabels[value] || value || '未分类'
}

function levelLabel(value: string) {
  return ({ error: '严重', warning: '警告', notice: '提示', P1: 'P1', P2: 'P2' } as Record<string, string>)[value] || value || '—'
}

function levelClass(value: string) {
  return value === 'error' || value === 'P1' ? 'tag-red' : value === 'warning' || value === 'P2' ? 'tag-amber' : 'tag-gray'
}

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function formatLatency(value?: number) {
  if (!value) return '—'
  return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${value}ms`
}

function buildListQuery() {
  const query = new URLSearchParams({ page: String(page.value), size: String(size.value) })
  if (summary.value) {
    query.set('start', summary.value.start)
    query.set('end', summary.value.end)
  }
  if (status.value) query.set('status', status.value)
  if (keyword.value.trim()) query.set('keyword', keyword.value.trim())
  if (scope.value === 'site') {
    if (category.value) query.set('category', category.value)
    if (level.value) query.set('level', level.value)
  } else {
    if (platform.value) query.set('platform', platform.value)
    if (errorType.value) query.set('error_type', errorType.value)
    if (keyword.value.trim()) query.set('request_id', keyword.value.trim())
  }
  return query
}

async function load() {
  loading.value = true
  try {
    summary.value = await api.get<ErrorCenterSummary>(`/api/admin/errors/summary?range=${range.value}`)
    const endpoint = scope.value === 'site' ? '/api/admin/errors/site' : '/api/admin/errors/api'
    const data = await api.get<{ items: ErrorItem[]; total: number }>(`${endpoint}?${buildListQuery()}`)
    total.value = data.total
    if (scope.value === 'site') siteItems.value = data.items as SiteErrorLog[]
    else apiItems.value = data.items as OpsErrorLog[]
  } finally {
    loading.value = false
  }
}

function selectScope(value: Scope) {
  scope.value = value
  page.value = 1
  selected.value = null
  category.value = ''
  level.value = ''
  platform.value = ''
  errorType.value = ''
  keyword.value = ''
}

function changePage(value: number) {
  page.value = value
  void load()
}

function applyFilters() {
  page.value = 1
  void load()
}

function resetFilters() {
  status.value = 'open'
  category.value = ''
  level.value = ''
  platform.value = ''
  errorType.value = ''
  keyword.value = ''
  page.value = 1
  void load()
}

async function resolve(item: ErrorItem) {
  const endpoint = scope.value === 'site' ? `/api/admin/errors/site/${item.id}/resolve` : `/api/admin/errors/api/${item.id}/resolve`
  const result = await withToast(() => api.post(endpoint, {}), '已标记处理')
  if (result !== null) {
    selected.value = null
    await load()
  }
}

watch(range, () => {
  page.value = 1
  void load()
})
watch(scope, () => void load())
onMounted(load)
</script>

<template>
  <div class="error-center-page ops-console">
    <header class="ops-console-head">
      <div class="ops-console-title">
        <h1>错误中心</h1>
        <span class="ops-console-state" :class="scopeSummary.open ? 'is-warning' : 'is-healthy'">
          <i></i>{{ scopeSummary.open ? `${scopeSummary.open} 条未处理` : '当前正常' }}
        </span>
      </div>
      <div class="ops-console-actions">
        <button class="btn-ghost" :disabled="loading" @click="load">{{ loading ? '刷新中' : '刷新' }}</button>
      </div>
    </header>

    <div class="ops-console-filter error-center-toolbar">
      <div class="ops-range-tabs">
        <button v-for="item in ['1h', '24h', '7d', '30d']" :key="item" :class="{ 'is-active': range === item }" @click="range = item">
          {{ ({ '1h': '1 小时', '24h': '24 小时', '7d': '7 天', '30d': '30 天' } as Record<string, string>)[item] }}
        </button>
      </div>
      <span class="ops-updated">更新于 {{ formatTime(summary?.generated_at) }}</span>
    </div>

    <div class="error-scope-switch" role="tablist" aria-label="错误范围">
      <button role="tab" :aria-selected="scope === 'site'" :class="{ 'is-active': scope === 'site' }" @click="selectScope('site')">
        <span>站点错误</span><strong>{{ summary?.site.total || 0 }}</strong>
        <small>页面、登录、管理端、支付与后台任务</small>
      </button>
      <button role="tab" :aria-selected="scope === 'api'" :class="{ 'is-active': scope === 'api' }" @click="selectScope('api')">
        <span>API 调用错误</span><strong>{{ summary?.api.total || 0 }}</strong>
        <small>中转请求、调度、账号与上游响应</small>
      </button>
    </div>

    <section class="error-metric-strip">
      <div><span>错误总数</span><strong>{{ scopeSummary.total }}</strong><small>当前时间范围</small></div>
      <div><span>未处理</span><strong class="text-amber">{{ scopeSummary.open }}</strong><small>{{ unresolvedRate.toFixed(1) }}%</small></div>
      <div><span>严重错误</span><strong class="text-signal-red">{{ scopeSummary.critical }}</strong><small>{{ scope === 'site' ? '运行异常 / 5xx' : 'P1 错误' }}</small></div>
      <div><span>最近一小时</span><strong>{{ scopeSummary.last_hour }}</strong><small>新增记录</small></div>
      <div v-if="scope === 'api'"><span>可重试</span><strong>{{ scopeSummary.retryable || 0 }}</strong><small>其中限流 {{ scopeSummary.business_limited || 0 }}</small></div>
      <div v-else><span>已处理</span><strong class="text-signal-green">{{ scopeSummary.resolved }}</strong><small>已归档</small></div>
    </section>

    <section class="error-center-workbench">
      <article class="ops-console-section error-category-panel">
        <header><h2>错误分类</h2><span>{{ scopeSummary.categories.length }} 类</span></header>
        <div class="error-category-list">
          <button
            v-for="item in scopeSummary.categories"
            :key="item.name"
            :class="{ 'is-active': (scope === 'site' ? category : errorType) === item.name }"
            @click="scope === 'site' ? category = item.name : errorType = item.name; applyFilters()"
          >
            <span>{{ categoryLabel(item.name) }}</span><strong>{{ item.count }}</strong>
            <i><b :style="{ width: `${item.count / maxCategoryCount * 100}%` }"></b></i>
          </button>
          <p v-if="!scopeSummary.categories.length">当前范围没有错误记录</p>
        </div>
      </article>

      <article class="ops-console-section error-filter-panel">
        <header><h2>{{ scope === 'site' ? '站点错误筛选' : 'API 错误筛选' }}</h2><button @click="resetFilters">重置</button></header>
        <div class="error-filter-grid">
          <select v-model="status" class="input">
            <option value="open">未处理</option><option value="resolved">已处理</option><option value="">全部状态</option>
          </select>
          <template v-if="scope === 'site'">
            <select v-model="category" class="input">
              <option value="">全部分类</option>
              <option v-for="item in scopeSummary.categories" :key="item.name" :value="item.name">{{ categoryLabel(item.name) }}</option>
            </select>
            <select v-model="level" class="input">
              <option value="">全部级别</option><option value="error">严重</option><option value="warning">警告</option><option value="notice">提示</option>
            </select>
            <input v-model="keyword" class="input" placeholder="错误码、页面、信息或请求编号" @keyup.enter="applyFilters" />
          </template>
          <template v-else>
            <select v-model="platform" class="input">
              <option value="">全部平台</option><option value="openai">OpenAI</option><option value="anthropic">Claude</option><option value="gemini">Gemini</option><option value="grok">Grok</option>
            </select>
            <select v-model="errorType" class="input">
              <option value="">全部类型</option>
              <option v-for="item in scopeSummary.categories" :key="item.name" :value="item.name">{{ categoryLabel(item.name) }}</option>
            </select>
            <input v-model="keyword" class="input" placeholder="完整请求编号" @keyup.enter="applyFilters" />
          </template>
          <button class="btn-primary" @click="applyFilters">筛选</button>
        </div>
      </article>
    </section>

    <section class="ops-console-section error-records">
      <header>
        <h2>{{ scope === 'site' ? '站点错误记录' : 'API 调用错误记录' }}</h2>
        <span>共 {{ total }} 条</span>
      </header>
      <div class="overflow-x-auto">
        <table v-if="scope === 'site'" v-responsive-table class="table-base error-center-table">
          <thead><tr><th>时间</th><th>级别 / 分类</th><th>页面 / 接口</th><th>错误</th><th>用户 / IP</th><th>请求编号</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in siteItems" :key="item.id">
              <td class="whitespace-nowrap text-xs text-slate-500">{{ formatTime(item.created_at) }}</td>
              <td><span :class="levelClass(item.level)">{{ levelLabel(item.level) }}</span><small>{{ categoryLabel(item.category || item.component) }}</small></td>
              <td><strong class="error-cell-path">{{ item.method || '—' }} {{ item.path || '—' }}</strong><small>{{ item.component }}</small></td>
              <td><strong class="error-cell-message">{{ item.message }}</strong><small>{{ item.error_code || (item.status_code ? `HTTP ${item.status_code}` : '前端运行错误') }}</small></td>
              <td><strong>{{ item.user_email || '未登录' }}</strong><small>{{ item.client_ip || '—' }}</small></td>
              <td><code>{{ item.request_id || '—' }}</code></td>
              <td><span :class="item.resolved_at ? 'tag-green' : 'tag-amber'">{{ item.resolved_at ? '已处理' : '未处理' }}</span></td>
              <td><button class="btn-ghost !px-2.5 !py-1 text-xs" @click="selected = item">详情</button></td>
            </tr>
            <tr v-if="!siteItems.length"><td colspan="8" class="py-10 text-center text-sm text-slate-500">当前筛选下没有站点错误</td></tr>
          </tbody>
        </table>

        <table v-else v-responsive-table class="table-base error-center-table">
          <thead><tr><th>时间</th><th>级别 / 类型</th><th>用户 / 密钥</th><th>模型 / 端点</th><th>分组 / 账号</th><th>错误</th><th>请求编号</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in apiItems" :key="item.id">
              <td class="whitespace-nowrap text-xs text-slate-500">{{ formatTime(item.created_at) }}</td>
              <td><span :class="levelClass(item.severity)">{{ item.severity }}</span><small>{{ categoryLabel(item.error_type) }}</small></td>
              <td><strong>{{ item.user_email || '—' }}</strong><small>{{ item.key_name || '未命名密钥' }}</small></td>
              <td><strong class="error-cell-path">{{ item.model || '—' }}</strong><small>{{ item.request_path || '—' }}</small></td>
              <td><strong>{{ item.group_name || '—' }}</strong><small>{{ item.account_name || '未选中账号' }}</small></td>
              <td><strong class="error-cell-message">{{ item.error_message || '请求失败' }}</strong><small>{{ item.status_code }} · {{ item.error_source }}</small></td>
              <td><code>{{ item.request_id || '—' }}</code></td>
              <td><span :class="item.resolved_at ? 'tag-green' : 'tag-amber'">{{ item.resolved_at ? '已处理' : '未处理' }}</span></td>
              <td><button class="btn-ghost !px-2.5 !py-1 text-xs" @click="selected = item">详情</button></td>
            </tr>
            <tr v-if="!apiItems.length"><td colspan="9" class="py-10 text-center text-sm text-slate-500">当前筛选下没有 API 调用错误</td></tr>
          </tbody>
        </table>
      </div>
      <Pagination :page="page" :size="size" :total="total" @change="changePage" />
    </section>

    <AppModal :open="Boolean(selected)" :title="scope === 'site' ? '站点错误详情' : 'API 调用错误详情'" width="wide" @close="selected = null">
      <div v-if="selectedSite" class="error-detail">
        <dl>
          <div><dt>发生时间</dt><dd>{{ formatTime(selectedSite.created_at) }}</dd></div>
          <div><dt>分类</dt><dd>{{ categoryLabel(selectedSite.category || selectedSite.component) }} · {{ levelLabel(selectedSite.level) }}</dd></div>
          <div><dt>页面 / 接口</dt><dd><code>{{ selectedSite.method }} {{ selectedSite.path }}</code></dd></div>
          <div><dt>状态 / 错误码</dt><dd>{{ selectedSite.status_code || '前端' }} · <code>{{ selectedSite.error_code || 'frontend.runtime_error' }}</code></dd></div>
          <div><dt>用户 / IP</dt><dd>{{ selectedSite.user_email || '未登录' }} · {{ selectedSite.client_ip || '—' }}</dd></div>
          <div><dt>请求编号</dt><dd><code>{{ selectedSite.request_id || '—' }}</code></dd></div>
          <div class="is-wide"><dt>浏览器</dt><dd>{{ selectedSite.user_agent || '—' }}</dd></div>
          <div class="is-wide"><dt>错误信息</dt><dd>{{ selectedSite.message }}</dd></div>
          <div v-if="selectedSite.details" class="is-wide"><dt>运行堆栈</dt><dd><pre>{{ selectedSite.details }}</pre></dd></div>
        </dl>
      </div>
      <div v-else-if="selectedAPI" class="error-detail">
        <dl>
          <div><dt>发生时间</dt><dd>{{ formatTime(selectedAPI.created_at) }}</dd></div>
          <div><dt>平台 / 模型</dt><dd>{{ PLATFORM_LABELS[selectedAPI.platform] || selectedAPI.platform }} · {{ selectedAPI.model }}</dd></div>
          <div><dt>错误链路</dt><dd>{{ selectedAPI.error_phase }} / {{ selectedAPI.error_type }} / {{ selectedAPI.error_source }}</dd></div>
          <div><dt>状态 / 耗时</dt><dd>HTTP {{ selectedAPI.status_code }} · {{ formatLatency(selectedAPI.duration_ms) }}</dd></div>
          <div><dt>用户 / 密钥</dt><dd>{{ selectedAPI.user_email || '—' }} · {{ selectedAPI.key_name || '—' }}</dd></div>
          <div><dt>分组 / 账号</dt><dd>{{ selectedAPI.group_name || '—' }} · {{ selectedAPI.account_name || '未选中账号' }}</dd></div>
          <div><dt>请求 IP</dt><dd>{{ selectedAPI.client_ip || '—' }} · {{ selectedAPI.ip_location || '地区未知' }}</dd></div>
          <div><dt>请求编号</dt><dd><code>{{ selectedAPI.request_id }}</code></dd></div>
          <div class="is-wide"><dt>错误信息</dt><dd>{{ selectedAPI.error_message }}</dd></div>
          <div v-if="selectedAPI.upstream_error_chain" class="is-wide"><dt>上游错误链</dt><dd><pre>{{ selectedAPI.upstream_error_chain }}</pre></dd></div>
        </dl>
      </div>
      <template #footer>
        <button class="btn-ghost" @click="selected = null">关闭</button>
        <button v-if="selected && !selected.resolved_at" class="btn-primary" @click="resolve(selected)">标记处理</button>
      </template>
    </AppModal>
  </div>
</template>
