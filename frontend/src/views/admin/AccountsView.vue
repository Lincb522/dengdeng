<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { AccountObservedUsage, AccountQuotaSnapshot, AccountQuotaWindow, Group, Proxy, UpstreamAccount } from '../../api/types'
import { PLATFORM_LABELS } from '../../api/types'
import { summarizeProviderError } from '../../api/errors'
import { useToast } from '../../stores/toast'
import Pagination from '../../components/Pagination.vue'

const toast = useToast()

const accounts = ref<UpstreamAccount[]>([])
const groups = ref<Group[]>([])
const proxies = ref<Proxy[]>([])
const totalAccounts = ref(0)
const page = ref(1)
const pageSize = 24
const filterGroup = ref<number | 0>(0)
const showForm = ref(false)
const editing = ref<UpstreamAccount | null>(null)
const diagnostic = ref<UpstreamAccount | null>(null)
const refreshingQuotaAccountID = ref<number | null>(null)
type AccountView = 'table' | 'cards'
type AccountSort = 'custom' | 'name' | 'platform' | 'group' | 'priority' | 'availability' | 'last_used'
const accountView = ref<AccountView>('table')
const sortBy = ref<AccountSort>('custom')
const sortDirection = ref<'asc' | 'desc'>('asc')
const filterPlatform = ref<'all' | 'openai' | 'anthropic' | 'gemini' | 'grok'>('all')
const filterAuthType = ref<'all' | 'api_key' | 'oauth'>('all')
const draggingAccountID = ref<number | null>(null)
const accountPresentationStorageKey = 'dengdeng.admin.accounts.presentation.v1'

const form = ref({
  group_id: 0,
  name: '',
  base_url: '',
  auth_type: 'api_key' as 'api_key' | 'oauth',
  api_key: '',
  access_token: '',
  refresh_token: '',
  account_id: '',
  email: '',
  proxy_id: 0,
  priority: 10,
	concurrency: 0,
  status: 'active',
})

type AccountPage = { items: UpstreamAccount[]; total: number; page: number; size: number }

function accountParams() {
  const query = new URLSearchParams({
    page: String(page.value),
    size: String(pageSize),
    sort: sortBy.value,
    order: sortDirection.value,
  })
  if (filterGroup.value) query.set('group_id', String(filterGroup.value))
  if (filterPlatform.value !== 'all') query.set('platform', filterPlatform.value)
  if (filterAuthType.value !== 'all') query.set('auth_type', filterAuthType.value)
  return query
}

async function loadAccounts() {
  const response = await api.get<AccountPage>(`/api/admin/accounts?${accountParams()}`)
  accounts.value = response.items || []
  totalAccounts.value = response.total || 0
}

async function load() {
  const [groupData, proxyData] = await Promise.all([
    api.get<Group[]>('/api/admin/groups'),
    api.get<Proxy[]>('/api/admin/proxies'),
  ])
  groups.value = groupData
  proxies.value = proxyData
  await loadAccounts()
}

function restoreAccountPresentation() {
  try {
    const saved = JSON.parse(localStorage.getItem(accountPresentationStorageKey) || '{}') as Partial<{
      view: AccountView
      sortBy: AccountSort
      sortDirection: 'asc' | 'desc'
    }>
    if (saved.view === 'table' || saved.view === 'cards') accountView.value = saved.view
    if (['custom', 'name', 'platform', 'group', 'priority', 'availability', 'last_used'].includes(saved.sortBy || '')) sortBy.value = saved.sortBy as AccountSort
    if (saved.sortDirection === 'asc' || saved.sortDirection === 'desc') sortDirection.value = saved.sortDirection
  } catch {
    // A malformed browser preference must never block account management.
  }
}

function persistAccountPresentation() {
  try {
    localStorage.setItem(accountPresentationStorageKey, JSON.stringify({
      view: accountView.value,
      sortBy: sortBy.value,
      sortDirection: sortDirection.value,
    }))
  } catch {
    // Preferences are optional in private or storage-restricted browsers.
  }
}

onMounted(() => {
  restoreAccountPresentation()
  void load()
})

const platformOfSelectedGroup = computed(
  () => groups.value.find((g) => g.id === form.value.group_id)?.platform ?? '',
)
// Only Claude and OpenAI have the built-in browser OAuth client. Gemini has no
// OAuth flow, and Grok's browser authorize needs an operator-supplied xAI
// client id, so Grok subscription accounts are added by JSON import instead.
const oauthAvailable = computed(
  () => platformOfSelectedGroup.value === 'anthropic' || platformOfSelectedGroup.value === 'openai',
)
const oauthProviderLabel = computed(() => PLATFORM_LABELS[platformOfSelectedGroup.value] || '上游账号')
const oauthStarting = ref(false)

function openCreate() {
  editing.value = null
  form.value = {
    group_id: groups.value[0]?.id ?? 0,
    name: '', base_url: '', auth_type: 'api_key',
    api_key: '', access_token: '', refresh_token: '', account_id: '', email: '', proxy_id: 0,
		priority: 10, concurrency: 0, status: 'active',
  }
  showForm.value = true
}

function openEdit(a: UpstreamAccount) {
  editing.value = a
  form.value = {
    group_id: a.group_id, name: a.name, base_url: a.base_url, auth_type: a.auth_type,
    api_key: '', access_token: '', refresh_token: '', account_id: a.account_id, email: a.email, proxy_id: a.proxy_id || 0,
		priority: a.priority, concurrency: a.concurrency || 0, status: a.status,
  }
  showForm.value = true
}

const canSave = computed(() => {
  if (!form.value.name) return false
  if (editing.value) return true
  return form.value.auth_type === 'api_key'
    ? !!form.value.api_key
    : !!(form.value.access_token || form.value.refresh_token)
})

async function save() {
  const body: Record<string, unknown> = {
    name: form.value.name,
    base_url: form.value.base_url,
    auth_type: form.value.auth_type,
    priority: Number(form.value.priority),
		concurrency: Math.max(0, Math.floor(Number(form.value.concurrency) || 0)),
    status: form.value.status,
    proxy_id: Number(form.value.proxy_id),
  }
  if (form.value.auth_type === 'api_key') {
    if (form.value.api_key) body.api_key = form.value.api_key
  } else {
    if (form.value.access_token) body.access_token = form.value.access_token
    if (form.value.refresh_token) body.refresh_token = form.value.refresh_token
    if (form.value.account_id) body.account_id = form.value.account_id
    if (form.value.email) body.email = form.value.email
  }
  let ok: unknown = null
  if (editing.value) {
    ok = await withToast(() => api.put(`/api/admin/accounts/${editing.value!.id}`, body), '已保存')
  } else {
    body.group_id = form.value.group_id
    ok = await withToast(() => api.post('/api/admin/accounts', body), '账号已添加')
  }
  if (ok !== null) {
    showForm.value = false
    await loadAccounts()
  }
}

async function remove(a: UpstreamAccount) {
  if (!confirm(`确认删除账号「${a.name}」?`)) return
  await withToast(() => api.delete(`/api/admin/accounts/${a.id}`), '已删除')
  if (accounts.value.length === 1 && page.value > 1) page.value--
  await loadAccounts()
}

async function startOAuthLogin() {
  if (editing.value || !form.value.group_id || !oauthAvailable.value) return
  // Open the window synchronously from the click event, otherwise browsers may
  // block it after the API request resolves.
  const popup = window.open('', 'dengdeng-oauth-login', 'width=520,height=720,noopener=false')
  oauthStarting.value = true
  try {
    const result = await api.post<{ authorize_url: string }>(
      `/api/admin/oauth/${platformOfSelectedGroup.value}/start`,
      {
        group_id: Number(form.value.group_id),
        name: form.value.name,
        base_url: form.value.base_url,
        priority: Number(form.value.priority),
				concurrency: Math.max(0, Math.floor(Number(form.value.concurrency) || 0)),
      },
    )
    if (popup) {
      popup.location.href = result.authorize_url
      popup.focus()
    } else {
      window.location.assign(result.authorize_url)
    }
  } catch (e) {
    popup?.close()
    toast.show(e instanceof Error ? e.message : '无法发起 OAuth 登录', 'error')
  } finally {
    oauthStarting.value = false
  }
}

function handleOAuthResult(data: { type?: string; result?: string; message?: string } | null) {
  if (data?.type !== 'dengdeng:oauth') return
  if (data.result === 'success') {
    toast.show(data.message || 'OAuth 登录成功，账号已添加', 'success')
    showForm.value = false
    void loadAccounts()
  } else {
    toast.show(data.message || 'OAuth 登录失败', 'error')
  }
}

function handleOAuthMessage(event: MessageEvent) {
  if (event.origin !== window.location.origin) return
  handleOAuthResult(event.data as { type?: string; result?: string; message?: string } | null)
}

function handleOAuthStorage(event: StorageEvent) {
  if (event.key !== 'dengdeng:oauth' || !event.newValue) return
  try {
    handleOAuthResult(JSON.parse(event.newValue) as { type?: string; result?: string; message?: string })
  } catch {
    // Ignore unrelated or malformed localStorage data.
  }
}

onMounted(() => {
  window.addEventListener('message', handleOAuthMessage)
  window.addEventListener('storage', handleOAuthStorage)
})
onBeforeUnmount(() => {
  window.removeEventListener('message', handleOAuthMessage)
  window.removeEventListener('storage', handleOAuthStorage)
})

// ---- import ----
const showImport = ref(false)
const imp = ref({ group_id: 0, format: 'auto', data: '', base_url: '', skip_expired: true })
const importFileInput = ref<HTMLInputElement | null>(null)
const importFileName = ref('')
type ImportPlatform = Group['platform']
const detectedImportPlatforms = ref<ImportPlatform[]>([])
type ImportResult = {
  imported: number
  skipped: number
  imported_names: string[]
  skipped_detail: { name: string; reason: string }[]
}
const impResult = ref<ImportResult | null>(null)

function openImport() {
  imp.value = { group_id: filterGroup.value || groups.value[0]?.id || 0, format: 'auto', data: '', base_url: '', skip_expired: true }
  importFileName.value = ''
  detectedImportPlatforms.value = []
  impResult.value = null
  showImport.value = true
}

function chooseImportFile() {
  importFileInput.value?.click()
}

async function readImportFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // The console API has a 1 MiB body limit; leave room for JSON escaping.
  if (!file) return
  if (file.size > 850 * 1024) {
    toast.show('JSON 文件不能超过 850 KB', 'error')
    input.value = ''
    return
  }
  try {
    const data = await file.text()
    JSON.parse(data)
    imp.value.data = data
    importFileName.value = file.name
    impResult.value = null
    matchImportGroup(data, true)
    toast.show(`已读取 ${file.name}`, 'success')
  } catch {
    toast.show('文件不是有效的 JSON', 'error')
  } finally {
    // Allow selecting the same corrected file again.
    input.value = ''
  }
}

function clearImportFile() {
  imp.value.data = ''
  importFileName.value = ''
  detectedImportPlatforms.value = []
  impResult.value = null
}

function normalizeImportPlatform(value: unknown): ImportPlatform | null {
  switch (String(value ?? '').trim().toLowerCase()) {
    case 'anthropic':
    case 'claude':
    case 'claudeai':
      return 'anthropic'
    case 'openai':
    case 'chatgpt':
    case 'codex':
      return 'openai'
    case 'gemini':
    case 'google':
      return 'gemini'
    case 'grok':
    case 'xai':
    case 'x.ai':
      return 'grok'
    default:
      return null
  }
}

function detectImportPlatforms(raw: string): ImportPlatform[] {
  try {
    const root = JSON.parse(raw) as unknown
    const found = new Set<ImportPlatform>()
    const inspect = (entry: unknown) => {
      if (!entry || typeof entry !== 'object') return
      const record = entry as Record<string, unknown>
      const explicit = normalizeImportPlatform(record.platform ?? record.provider ?? record.type)
      if (explicit) found.add(explicit)
      if (record.claudeAiOauth || record.claude_ai_oauth) found.add('anthropic')
      // Native Codex auth.json has `tokens` / `auth_mode` but no platform.
      if (record.tokens || record.auth_mode === 'chatgpt' || record.authMode === 'chatgpt') found.add('openai')
    }
    if (Array.isArray(root)) {
      root.forEach(inspect)
    } else if (root && typeof root === 'object') {
      const record = root as Record<string, unknown>
      const accounts = record.accounts ?? (record.data as Record<string, unknown> | undefined)?.accounts
      if (Array.isArray(accounts)) accounts.forEach(inspect)
      else inspect(root)
    }
    return [...found]
  } catch {
    return []
  }
}

function matchImportGroup(raw: string, notify: boolean) {
  const platforms = detectImportPlatforms(raw)
  detectedImportPlatforms.value = platforms
  if (platforms.length !== 1) return
  const platform = platforms[0]
  const matches = groups.value.filter((group) => group.platform === platform && group.status === 'active')
  if (!matches.length) {
    if (notify) toast.show(`未找到可用的 ${PLATFORM_LABELS[platform]} 分组，请先创建该平台分组`, 'error')
    return
  }
  // Keep an explicitly selected compatible group. A workspace can have more
  // than one group for a platform, and silently choosing the first one leaves
  // API keys in the intended group with no accounts and causes a misleading
  // 503 from the scheduler.
  const selected = groups.value.find((group) => group.id === imp.value.group_id)
  if (selected?.status === 'active' && selected.platform === platform) {
    if (notify) toast.show(`已识别为 ${PLATFORM_LABELS[platform]} JSON，将导入到「${selected.name}」`, 'success')
    return
  }
  if (matches.length === 1) {
    imp.value.group_id = matches[0].id
    if (notify) toast.show(`已识别为 ${PLATFORM_LABELS[platform]} JSON，目标分组已切换为「${matches[0].name}」`, 'success')
    return
  }
  if (notify) toast.show(`已识别为 ${PLATFORM_LABELS[platform]} JSON，请在多个同平台分组中确认目标分组`, 'error')
}

async function doImport() {
  matchImportGroup(imp.value.data, true)
  const res = await withToast(
    () => api.post<ImportResult>('/api/admin/accounts/import', {
      group_id: Number(imp.value.group_id),
      format: imp.value.format,
      data: imp.value.data,
      base_url: imp.value.base_url,
      skip_expired: imp.value.skip_expired,
    }),
    '导入完成',
  )
  if (res) {
    impResult.value = res
    page.value = 1
    await loadAccounts()
  }
}

function healthState(a: UpstreamAccount): { label: string; cls: string } {
  if (a.status !== 'active') return { label: '停用', cls: 'tag-gray' }
	if (accountQuotaExhausted(a.quota)) return { label: '额度用尽', cls: 'tag-red' }
  if (a.cooldown_until && new Date(a.cooldown_until) > new Date()) return { label: '冷却中', cls: 'tag-red' }
  return { label: '在线', cls: 'tag-green' }
}

function authBadge(a: UpstreamAccount): { label: string; cls: string } {
  return a.auth_type === 'oauth'
    ? { label: 'OAuth', cls: 'tag-cyan' }
    : { label: 'API Key', cls: 'tag-gray' }
}

function tokenExpiryInfo(a: UpstreamAccount): { text: string; cls: string } | null {
  if (a.auth_type !== 'oauth' || !a.expires_at) return null
  const t = new Date(a.expires_at)
  const expired = t.getTime() < Date.now()
	return { text: t.toLocaleString(), cls: expired ? 'text-signal-red' : 'text-slate-500' }
}

function subscriptionExpiryInfo(a: UpstreamAccount): { text: string; cls: string } | null {
	if (!a.quota?.subscription_expires_at) return null
	const expiry = new Date(a.quota.subscription_expires_at)
	if (Number.isNaN(expiry.getTime())) return null
	const expired = expiry.getTime() < Date.now()
	return { text: expiry.toLocaleString(), cls: expired ? 'text-signal-red' : 'text-slate-500' }
}

function quotaPercent(value: number) {
	return Math.min(100, Math.max(0, Number(value) || 0))
}

function quotaState(snapshot?: AccountQuotaSnapshot): { label: string; cls: string } {
	if (!snapshot) return { label: '等待同步', cls: 'tag-gray' }
	switch (snapshot.state) {
		case 'ready': return { label: '已同步', cls: 'tag-green' }
		case 'partial': return { label: '部分可用', cls: 'tag-amber' }
		case 'error': return { label: '刷新失败', cls: 'tag-red' }
		default: return { label: '本站记录', cls: 'tag-gray' }
	}
}

function quotaSourceLabel(snapshot?: AccountQuotaSnapshot) {
	if (!snapshot) return '上游额度'
	if (snapshot.plan_type) return snapshot.plan_type
	switch (snapshot.source) {
		case 'codex_subscription': return 'Codex 订阅'
		case 'claude_subscription': return 'Claude 订阅'
		case 'grok_billing': return 'Grok 订阅'
		case 'rate_limit_headers': return '上游限额'
		default: return `${PLATFORM_LABELS[snapshot.platform] || '上游'} 用量`
	}
}

function quotaWindowText(window: AccountQuotaWindow) {
	if (window.used_percent !== undefined && window.used_percent !== null) {
		return `${window.label} · 已用 ${quotaPercent(window.used_percent).toFixed(1)}%`
	}
	if (window.remaining !== undefined && window.limit !== undefined) {
		return `${window.label} · 剩余 ${formatQuotaNumber(window.remaining)} / ${formatQuotaNumber(window.limit)} ${quotaUnit(window.unit)}`
	}
	if (window.remaining !== undefined) {
		return `${window.label} · 剩余 ${formatQuotaNumber(window.remaining)} ${quotaUnit(window.unit)}`
	}
	return window.label
}

function quotaUnit(unit?: string) {
	if (unit === 'requests') return '次'
	if (unit === 'tokens') return 'Tokens'
	return unit || ''
}

function formatQuotaNumber(value: number) {
	return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value)
}

function quotaResetText(window: AccountQuotaWindow) {
	if (!window.reset_at) return ''
	const reset = new Date(window.reset_at)
	if (Number.isNaN(reset.getTime())) return ''
	const remainingMs = reset.getTime() - Date.now()
	const absolute = reset.toLocaleString()
	if (remainingMs <= 0) return `已于 ${absolute} 重置`
	const totalMinutes = Math.ceil(remainingMs / 60_000)
	if (totalMinutes < 60) return `${totalMinutes} 分钟后重置（${absolute}）`
	const hours = Math.floor(totalMinutes / 60)
	const minutes = totalMinutes % 60
	return `${hours} 小时${minutes ? ` ${minutes} 分` : ''}后重置（${absolute}）`
}

function accountQuotaExhausted(quota?: AccountQuotaSnapshot) {
	return !!quota?.windows?.some((window) => window.used_percent !== undefined && quotaPercent(window.used_percent) >= 100)
}

function primaryObservedUsage(quota?: AccountQuotaSnapshot): AccountObservedUsage | undefined {
	return quota?.observed_usage?.find((item) => item.key === '24h') || quota?.observed_usage?.[0]
}

function observedUsageText(usage?: AccountObservedUsage) {
	if (!usage) return '暂无本站调用记录'
	const tokens = Number(usage.input_tokens || 0) + Number(usage.output_tokens || 0)
	return `${usage.label} · ${formatQuotaNumber(usage.requests)} 次 · ${formatQuotaNumber(tokens)} Tokens`
}

function quotaCheckedAt(quota?: AccountQuotaSnapshot) {
	const raw = quota?.fetched_at || quota?.last_attempt_at
	return raw ? new Date(raw).toLocaleString() : '等待自动刷新'
}

function availability(a: UpstreamAccount): { score: number; label: string; cls: string; reason: string } {
	if (a.status !== 'active') return { score: 0, label: '已停用', cls: 'tag-gray', reason: '管理员已停用该账号' }
	if (a.auth_type === 'oauth' && a.expires_at && new Date(a.expires_at) <= new Date() && a.quota?.state === 'error') return { score: 0, label: '续期失败', cls: 'tag-red', reason: 'OAuth Token 过期且自动续期失败，需重新授权' }
	if (accountQuotaExhausted(a.quota)) return { score: 0, label: '额度用尽', cls: 'tag-red', reason: '上游返回该账号的额度窗口已用尽' }
	if (a.cooldown_until && new Date(a.cooldown_until) > new Date()) return { score: 10, label: '冷却中', cls: 'tag-red', reason: `预计 ${new Date(a.cooldown_until).toLocaleTimeString()} 后恢复调度` }
	if (a.error_count >= 4) return { score: 45, label: '需关注', cls: 'tag-amber', reason: `近期连续失败 ${a.error_count} 次` }
	if (a.error_count > 0) return { score: 75, label: '待观察', cls: 'tag-amber', reason: `近期失败 ${a.error_count} 次，当前仍可调度` }
	return { score: 100, label: '可调度', cls: 'tag-green', reason: '状态正常，可参与上游调度' }
}

function openDiagnostic(account: UpstreamAccount) {
	diagnostic.value = account
}

// Sorting and filtering happen in the API so each page reflects the same
// global order, rather than sorting only the records already in the browser.
const sortedAccounts = computed(() => accounts.value)

const manualOrderEnabled = computed(() => sortBy.value === 'custom')

function updateAccountView(view: AccountView) {
	accountView.value = view
	persistAccountPresentation()
}

function updateSort() {
	persistAccountPresentation()
	page.value = 1
	void loadAccounts()
}

function toggleSortDirection() {
	sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'
	persistAccountPresentation()
	page.value = 1
	void loadAccounts()
}

function updateAccountFilters() {
	page.value = 1
	void loadAccounts()
}

function changePage(nextPage: number) {
	page.value = nextPage
	void loadAccounts()
}

function beginAccountDrag(account: UpstreamAccount) {
	if (!manualOrderEnabled.value) return
	draggingAccountID.value = account.id
}

function endAccountDrag() {
	draggingAccountID.value = null
}

async function dropAccountAt(target: UpstreamAccount) {
	const sourceID = draggingAccountID.value
	endAccountDrag()
	if (!manualOrderEnabled.value || !sourceID || sourceID === target.id) return
	const visibleOrder = sortedAccounts.value
	const sourceIndex = visibleOrder.findIndex((account) => account.id === sourceID)
	const targetIndex = visibleOrder.findIndex((account) => account.id === target.id)
	if (sourceIndex < 0 || targetIndex < 0) return
	const placement = sourceIndex < targetIndex ? 'after' : 'before'
	await withToast(
		() => api.put('/api/admin/accounts/order', { source_id: sourceID, target_id: target.id, placement }),
		'自定义排序已保存',
	)
	await loadAccounts()
}

async function refreshAccountQuota(account: UpstreamAccount) {
	if (refreshingQuotaAccountID.value) return
	refreshingQuotaAccountID.value = account.id
	try {
		const snapshot = await api.post<AccountQuotaSnapshot>(`/api/admin/accounts/${account.id}/quota/refresh`, {})
		account.quota = snapshot
		toast.show(snapshot.state === 'error' ? snapshot.message : '额度与用量已刷新', snapshot.state === 'error' ? 'error' : 'success')
	} catch (error) {
		toast.show(error instanceof Error ? error.message : '额度查询失败', 'error')
	} finally {
		refreshingQuotaAccountID.value = null
	}
}

</script>

<template>
  <div>
    <div class="console-page-head accounts-page-head">
      <div>
        <h1>上游账号</h1>
        <p class="mt-1 text-sm text-slate-500">按分组、类别和状态管理账号；自定义排序不会影响上游调度优先级。</p>
      </div>
      <div class="accounts-toolbar">
        <select v-model.number="filterGroup" class="input accounts-toolbar-select" aria-label="分组筛选" @change="updateAccountFilters">
          <option :value="0">全部分组</option>
          <option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option>
        </select>
        <select v-model="filterPlatform" class="input accounts-toolbar-select" aria-label="平台类别筛选" @change="updateAccountFilters">
          <option value="all">全部平台</option>
          <option value="openai">OpenAI</option>
          <option value="anthropic">Claude</option>
          <option value="gemini">Gemini</option>
          <option value="grok">Grok</option>
        </select>
        <select v-model="filterAuthType" class="input accounts-toolbar-select" aria-label="凭证类型筛选" @change="updateAccountFilters">
          <option value="all">全部凭证</option>
          <option value="api_key">API Key</option>
          <option value="oauth">OAuth</option>
        </select>
        <select v-model="sortBy" class="input accounts-toolbar-select" @change="updateSort">
          <option value="custom">自定义排序</option>
          <option value="name">账号名称</option>
          <option value="platform">平台类别</option>
          <option value="group">所属分组</option>
          <option value="priority">调度优先级</option>
          <option value="availability">可用度</option>
          <option value="last_used">最近使用</option>
        </select>
        <button class="accounts-sort-direction" type="button" :disabled="sortBy === 'custom'" :title="sortDirection === 'asc' ? '切换为降序' : '切换为升序'" @click="toggleSortDirection">{{ sortDirection === 'asc' ? '升序' : '降序' }}</button>
        <div class="accounts-view-toggle" role="group" aria-label="账号展示方式">
          <button type="button" :class="{ 'is-active': accountView === 'table' }" @click="updateAccountView('table')">列表</button>
          <button type="button" :class="{ 'is-active': accountView === 'cards' }" @click="updateAccountView('cards')">卡片</button>
        </div>
        <button class="btn-ghost" :disabled="!groups.length" @click="openImport">导入 JSON</button>
        <button class="btn-primary" :disabled="!groups.length" @click="openCreate">添加账号</button>
      </div>
    </div>

    <div v-if="manualOrderEnabled" class="account-order-hint">拖拽可调整当前页账号的展示顺序；保存时会在全量账号中原子更新。该顺序仅用于控制台，不会影响接口请求的调度优先级。</div>

    <div v-if="accountView === 'cards'" class="account-card-grid">
      <article
        v-for="a in sortedAccounts"
        :key="a.id"
        class="account-card"
        :class="{ 'is-draggable': manualOrderEnabled, 'is-dragging': draggingAccountID === a.id }"
        :draggable="manualOrderEnabled"
        @dragstart="beginAccountDrag(a)"
        @dragend="endAccountDrag"
        @dragover.prevent
        @drop="dropAccountAt(a)"
      >
        <header class="account-card-head">
          <div class="min-w-0">
            <div class="flex min-w-0 items-center gap-2">
              <h2 class="truncate" :title="a.name">{{ a.name }}</h2>
              <span :class="healthState(a).cls" class="shrink-0">{{ healthState(a).label }}</span>
            </div>
            <p class="truncate" :title="a.email || a.base_url || '官方默认'">{{ a.email || a.base_url || '官方默认' }}</p>
          </div>
          <span :class="availability(a).cls" class="shrink-0 whitespace-nowrap">{{ availability(a).score }}%</span>
        </header>

        <div class="account-card-tags">
          <span class="tag-gray">{{ PLATFORM_LABELS[a.platform] }}</span>
          <span :class="authBadge(a).cls">{{ authBadge(a).label }}</span>
          <span :class="a.proxy ? 'tag-cyan' : 'tag-gray'" class="max-w-[11rem] truncate" :title="a.proxy?.name || '默认出口'">{{ a.proxy?.name || '默认出口' }}</span>
        </div>

        <dl class="account-card-meta">
          <div><dt>分组</dt><dd class="truncate" :title="a.group?.name">{{ a.group?.name || '未分组' }}</dd></div>
          <div><dt>优先级</dt><dd class="num">{{ a.priority }}</dd></div>
							<div><dt>并发上限</dt><dd class="num">{{ a.concurrency > 0 ? a.concurrency : '不限' }}</dd></div>
          <div><dt>最近使用</dt><dd>{{ a.last_used_at ? new Date(a.last_used_at).toLocaleString() : '从未使用' }}</dd></div>
          <div><dt>可用度</dt><dd :class="availability(a).cls">{{ availability(a).label }}</dd></div>
        </dl>

        <section class="account-card-quota">
          <template v-if="a.quota">
            <div class="account-quota-head"><strong>{{ quotaSourceLabel(a.quota) }}</strong><span :class="quotaState(a.quota).cls">{{ quotaState(a.quota).label }}</span></div>
			<p v-if="subscriptionExpiryInfo(a)" class="account-quota-message" :class="subscriptionExpiryInfo(a)!.cls">套餐到期 {{ subscriptionExpiryInfo(a)!.text }}</p>
            <div v-for="window in a.quota.windows || []" :key="window.key" class="account-card-quota-window">
              <span>{{ quotaWindowText(window) }}</span>
              <div v-if="window.used_percent !== undefined"><i class="bg-amber" :style="{ width: `${quotaPercent(window.used_percent)}%` }"></i></div>
              <small v-if="quotaResetText(window)">{{ quotaResetText(window) }}</small>
            </div>
            <p v-if="a.quota.message" class="account-quota-message">{{ a.quota.message }}</p>
            <div class="account-quota-observed"><span>{{ observedUsageText(primaryObservedUsage(a.quota)) }}</span><span>{{ quotaCheckedAt(a.quota) }}</span></div>
          </template>
          <p v-else>等待后台自动同步额度与用量。</p>
          <button type="button" :disabled="refreshingQuotaAccountID === a.id" @click="refreshAccountQuota(a)">{{ refreshingQuotaAccountID === a.id ? '刷新中…' : '刷新额度' }}</button>
        </section>

        <footer class="account-card-actions">
          <button class="btn-ghost !px-3 !py-1.5 text-xs" @click="openDiagnostic(a)">诊断</button>
          <button class="btn-ghost !px-3 !py-1.5 text-xs" @click="openEdit(a)">编辑</button>
          <button class="btn-danger !px-3 !py-1.5 text-xs" @click="remove(a)">删除</button>
        </footer>
      </article>
      <div v-if="!sortedAccounts.length" class="account-empty-state">{{ groups.length ? '当前筛选下没有账号' : '请先在「分组管理」创建分组' }}</div>
    </div>

    <div v-else class="card overflow-x-auto">
      <table class="table-base">
        <thead>
          <tr>
            <th>名称</th>
            <th>分组</th>
            <th>凭据</th>
            <th>Base URL</th>
            <th>代理</th>
            <th>优先级</th>
							<th>并发</th>
			<th>上游额度 / 用量</th>
            <th>可用度</th>
            <th>最后使用</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in sortedAccounts" :key="a.id" :class="{ 'cursor-grab': manualOrderEnabled, 'opacity-55': draggingAccountID === a.id }" :draggable="manualOrderEnabled" @dragstart="beginAccountDrag(a)" @dragend="endAccountDrag" @dragover.prevent @drop="dropAccountAt(a)">
            <td>
              <div class="font-medium text-slate-200">{{ a.name }}</div>
              <div v-if="a.email" class="text-xs text-slate-500">{{ a.email }}</div>
            </td>
            <td>
              <span class="tag-gray">{{ a.group?.name }}</span>
              <span class="ml-1 text-xs text-slate-500">{{ PLATFORM_LABELS[a.platform] }}</span>
            </td>
            <td>
              <span :class="authBadge(a).cls">{{ authBadge(a).label }}</span>
			  <div v-if="subscriptionExpiryInfo(a)" class="mt-1 whitespace-nowrap text-xs" :class="subscriptionExpiryInfo(a)!.cls" title="上游套餐到期时间">
				套餐到期 {{ subscriptionExpiryInfo(a)!.text }}
              </div>
            </td>
            <td class="max-w-[200px] truncate font-mono text-xs text-slate-400" :title="a.base_url">{{ a.base_url || '官方默认' }}</td>
            <td class="min-w-[8.5rem] max-w-[11rem] whitespace-nowrap">
              <span :class="[a.proxy ? 'tag-cyan' : 'tag-gray', 'max-w-full truncate whitespace-nowrap align-middle']" :title="a.proxy?.name || '默认出口'">{{ a.proxy?.name || '默认出口' }}</span>
            </td>
            <td class="num">{{ a.priority }}</td>
							<td class="num">{{ a.concurrency > 0 ? a.concurrency : '不限' }}</td>
			<td class="min-w-56">
				<div v-if="a.quota" class="space-y-2">
					<div class="flex items-center gap-2 whitespace-nowrap"><strong class="text-xs text-slate-200">{{ quotaSourceLabel(a.quota) }}</strong><span :class="quotaState(a.quota).cls">{{ quotaState(a.quota).label }}</span></div>
					<div v-if="subscriptionExpiryInfo(a)" class="whitespace-nowrap text-[11px]" :class="subscriptionExpiryInfo(a)!.cls">套餐到期 {{ subscriptionExpiryInfo(a)!.text }}</div>
					<div v-for="window in a.quota.windows || []" :key="window.key" class="min-w-52">
						<div class="num whitespace-nowrap text-xs text-slate-300">{{ quotaWindowText(window) }}</div>
						<div v-if="window.used_percent !== undefined" class="mt-1 h-1.5 overflow-hidden rounded-full bg-ink-800"><span class="block h-full rounded-full bg-amber transition-[width] duration-200" :style="{ width: `${quotaPercent(window.used_percent)}%` }"></span></div>
					</div>
					<div class="whitespace-nowrap text-[11px] text-slate-500">{{ observedUsageText(primaryObservedUsage(a.quota)) }}</div>
					<div class="whitespace-nowrap text-[11px] text-slate-500">{{ quotaCheckedAt(a.quota) }}</div>
				</div>
				<div v-else class="whitespace-nowrap text-xs text-slate-500">等待后台自动同步</div>
				<button class="mt-2 whitespace-nowrap text-xs text-amber hover:text-amber-light disabled:opacity-50" :disabled="refreshingQuotaAccountID === a.id" @click="refreshAccountQuota(a)">{{ refreshingQuotaAccountID === a.id ? '刷新中…' : '刷新额度' }}</button>
			</td>
            <td class="min-w-[8.5rem] whitespace-nowrap">
              <span :class="[availability(a).cls, 'whitespace-nowrap']">{{ availability(a).score }}% · {{ availability(a).label }}</span>
              <div v-if="a.error_count" class="mt-1 text-[11px] text-slate-500 whitespace-nowrap">近期失败 {{ a.error_count }} 次</div>
            </td>
            <td class="text-xs text-slate-500">{{ a.last_used_at ? new Date(a.last_used_at).toLocaleString() : '从未' }}</td>
            <td class="text-right">
				<button class="btn-ghost !px-2.5 !py-1 text-xs" @click="openDiagnostic(a)">诊断</button>
              <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="openEdit(a)">编辑</button>
              <button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="remove(a)">删除</button>
            </td>
          </tr>
          <tr v-if="!sortedAccounts.length">
			<td colspan="11" class="py-10 text-center text-sm text-slate-500">
              {{ groups.length ? '暂无账号' : '请先在「分组管理」创建分组' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <Pagination :page="page" :size="pageSize" :total="totalAccounts" @change="changePage" />

    <!-- create / edit -->
    <Teleport to="body">
      <div v-if="showForm" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="showForm = false">
        <div class="card w-full max-w-md p-6">
          <h3 class="mb-5 text-base font-semibold text-slate-100">{{ editing ? '编辑账号' : '添加上游账号' }}</h3>
          <div class="space-y-4">
            <div v-if="!editing">
              <label class="label">所属分组</label>
              <select v-model.number="form.group_id" class="input">
                <option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }} ({{ PLATFORM_LABELS[g.platform] }})</option>
              </select>
            </div>
            <div>
              <label class="label">凭据类型</label>
              <div class="flex gap-2">
                <button
                  type="button"
                  class="flex-1 rounded-lg border px-3 py-2 text-sm transition"
                  :class="form.auth_type === 'api_key' ? 'border-amber bg-amber/10 text-amber' : 'border-ink-600 text-slate-400'"
                  @click="form.auth_type = 'api_key'"
                >API Key</button>
                <button
                  type="button"
                  class="flex-1 rounded-lg border px-3 py-2 text-sm transition disabled:opacity-40"
                  :class="form.auth_type === 'oauth' ? 'border-amber bg-amber/10 text-amber' : 'border-ink-600 text-slate-400'"
                  :disabled="!oauthAvailable"
                  :title="oauthAvailable ? '' : 'Gemini 暂不支持 OAuth 自动续期'"
                  @click="form.auth_type = 'oauth'"
                >OAuth</button>
              </div>
            </div>
            <div>
              <label class="label">账号名称</label>
              <input v-model="form.name" class="input" placeholder="例如:key-01 或邮箱" />
            </div>
            <div>
              <label class="label">Base URL(留空用官方地址)</label>
              <input v-model="form.base_url" class="input font-mono" placeholder="https://api.anthropic.com" />
            </div>
			<div>
			  <label class="label">单独代理</label>
			  <select v-model.number="form.proxy_id" class="input">
				<option :value="0">不使用（默认出口）</option>
				<option v-for="proxy in proxies.filter((item) => item.status === 'active' || item.id === form.proxy_id)" :key="proxy.id" :value="proxy.id">{{ proxy.name }} · {{ proxy.protocol }}://{{ proxy.host }}:{{ proxy.port }}{{ proxy.status !== 'active' ? '（已停用）' : '' }}</option>
			  </select>
			  <p class="mt-1 text-xs text-slate-500">代理在「代理配置」中单独维护。</p>
			</div>

            <template v-if="form.auth_type === 'api_key'">
              <div>
                <label class="label">API Key {{ editing ? '(留空保持不变)' : '' }}</label>
                <input v-model="form.api_key" class="input font-mono" placeholder="sk-..." />
              </div>
            </template>
            <template v-else>
              <div v-if="!editing" class="rounded-lg border border-signal-cyan/30 bg-signal-cyan/5 p-3">
                <div class="flex items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-slate-200">直接登录 {{ oauthProviderLabel }}</p>
                    <p class="mt-0.5 text-xs text-slate-500">在新窗口完成授权后，凭据会自动加密保存。</p>
                  </div>
                  <button
                    type="button"
                    class="btn-primary shrink-0 !px-3 !py-1.5 text-xs"
                    :disabled="!oauthAvailable || oauthStarting"
                    @click="startOAuthLogin"
                  >{{ oauthStarting ? '正在跳转…' : '去登录' }}</button>
                </div>
                <p v-if="!oauthAvailable" class="mt-2 text-xs text-signal-red">请先选择支持 OAuth 的 Claude 或 OpenAI 分组。</p>
              </div>
              <div v-if="!editing" class="flex items-center gap-3 text-xs text-slate-600"><span class="h-px flex-1 bg-ink-600"></span><span>或手动录入凭据</span><span class="h-px flex-1 bg-ink-600"></span></div>
              <div>
                <label class="label">Access Token {{ editing ? '(留空保持不变)' : '' }}</label>
                <textarea v-model="form.access_token" rows="2" class="input font-mono text-xs" placeholder="eyJ..."></textarea>
              </div>
              <div>
                <label class="label">Refresh Token(用于自动续期)</label>
                <textarea v-model="form.refresh_token" rows="2" class="input font-mono text-xs" placeholder="缺失则过期后需重新导入"></textarea>
              </div>
              <div class="grid grid-cols-2 gap-4">
                <div>
                  <label class="label">Account ID(可选)</label>
                  <input v-model="form.account_id" class="input font-mono text-xs" placeholder="chatgpt_account_id" />
                </div>
                <div>
                  <label class="label">邮箱(可选)</label>
                  <input v-model="form.email" class="input text-xs" placeholder="you@example.com" />
                </div>
              </div>
            </template>

            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label">优先级(大者优先)</label>
                <input v-model.number="form.priority" type="number" class="input" />
              </div>
              <div>
                <label class="label">状态</label>
                <select v-model="form.status" class="input">
                  <option value="active">启用</option>
                  <option value="disabled">停用</option>
                </select>
              </div>
            </div>
							<div>
								<label class="label">账号并发上限</label>
								<input v-model.number="form.concurrency" type="number" min="0" max="10000" step="1" class="input" placeholder="0 = 不限制" />
								<p class="mt-1 text-xs text-slate-500">达到上限后请求会等待可用槽位；0 表示不限。</p>
							</div>
            <div class="flex justify-end gap-3 pt-2">
              <button class="btn-ghost" @click="showForm = false">取消</button>
              <button class="btn-primary" :disabled="!canSave" @click="save">保存</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

		<Teleport to="body">
			<div v-if="diagnostic" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="diagnostic = null">
				<div class="card max-h-[88vh] w-full max-w-lg overflow-y-auto p-6">
					<div class="mb-5 flex items-start justify-between gap-4"><div><h3 class="text-base font-semibold text-slate-100">账号诊断</h3><p class="mt-1 text-sm text-slate-500">{{ diagnostic.name }} · {{ diagnostic.group?.name || '未分组' }}</p></div><button class="btn-ghost !px-2.5 !py-1 text-xs" @click="diagnostic = null">关闭</button></div>
					<div class="grid gap-3 sm:grid-cols-2">
						<section class="rounded-lg border border-ink-700 bg-ink-850 p-3">
							<p class="text-xs text-slate-500">调度可用度</p>
							<div class="mt-1 flex items-center gap-2"><strong class="num text-xl text-slate-100">{{ availability(diagnostic).score }}%</strong><span :class="availability(diagnostic).cls">{{ availability(diagnostic).label }}</span></div>
							<p class="mt-2 text-xs leading-5 text-slate-500">{{ availability(diagnostic).reason }}</p>
						</section>
						<section class="rounded-lg border border-ink-700 bg-ink-850 p-3">
							<div class="flex items-center justify-between gap-2"><p class="text-xs text-slate-500">上游额度与用量</p><span :class="quotaState(diagnostic.quota).cls">{{ quotaState(diagnostic.quota).label }}</span></div>
							<template v-if="diagnostic.quota">
								<strong class="mt-2 block text-sm text-slate-100">{{ quotaSourceLabel(diagnostic.quota) }}</strong>
								<div v-for="window in diagnostic.quota.windows || []" :key="window.key" class="mt-2">
									<p class="text-xs text-slate-300">{{ quotaWindowText(window) }}</p>
									<div v-if="window.used_percent !== undefined" class="mt-1 h-1.5 overflow-hidden rounded-full bg-ink-800"><span class="block h-full rounded-full bg-amber" :style="{ width: `${quotaPercent(window.used_percent)}%` }"></span></div>
									<p v-if="quotaResetText(window)" class="mt-1 text-xs text-slate-500">{{ quotaResetText(window) }}</p>
								</div>
								<p class="mt-2 text-xs text-slate-500">{{ observedUsageText(primaryObservedUsage(diagnostic.quota)) }}</p>
								<p v-if="subscriptionExpiryInfo(diagnostic)" class="mt-1 text-xs" :class="subscriptionExpiryInfo(diagnostic)!.cls">套餐到期：{{ subscriptionExpiryInfo(diagnostic)!.text }}</p>
								<p v-if="tokenExpiryInfo(diagnostic)" class="mt-1 text-xs text-slate-500">Token 有效至：{{ tokenExpiryInfo(diagnostic)!.text }}（会自动续期）</p>
								<p v-if="diagnostic.quota.last_credential_refresh" class="mt-1 text-xs text-slate-500">凭证已自动续期：{{ new Date(diagnostic.quota.last_credential_refresh).toLocaleString() }}</p>
								<p v-if="diagnostic.quota.message" class="mt-2 text-xs leading-5 text-slate-500">{{ diagnostic.quota.message }}</p>
							</template>
							<p v-else class="mt-2 text-xs leading-5 text-slate-500">等待后台自动同步。</p>
							<button class="mt-3 text-xs text-amber hover:text-amber-light disabled:opacity-50" :disabled="refreshingQuotaAccountID === diagnostic.id" @click="refreshAccountQuota(diagnostic)">{{ refreshingQuotaAccountID === diagnostic.id ? '刷新中…' : '刷新额度' }}</button>
						</section>
					</div>
					<section class="mt-3 rounded-lg border border-ink-700 bg-ink-850 p-3"><p class="text-xs text-slate-500">账号状态</p><div class="mt-2 flex flex-wrap items-center gap-2"><span :class="healthState(diagnostic).cls">{{ healthState(diagnostic).label }}</span><span v-if="diagnostic.cooldown_until" class="text-xs text-slate-500">冷却至 {{ new Date(diagnostic.cooldown_until).toLocaleString() }}</span><span class="text-xs text-slate-500">最近使用：{{ diagnostic.last_used_at ? new Date(diagnostic.last_used_at).toLocaleString() : '从未' }}</span></div></section>
					<section v-if="diagnostic.last_error" class="mt-3 rounded-lg border border-signal-red/25 bg-signal-red/5 p-3"><p class="text-xs font-semibold text-signal-red">最近错误</p><p class="mt-1 text-sm text-slate-200">{{ summarizeProviderError(diagnostic.last_error, 180) }}</p><details class="mt-3"><summary class="cursor-pointer text-xs text-slate-500">查看原始诊断信息</summary><pre class="mt-2 max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-md bg-ink-950 p-3 text-xs text-slate-300">{{ diagnostic.last_error }}</pre></details></section>
					<p v-else class="mt-4 text-sm text-signal-green">暂无近期错误记录。</p>
				</div>
			</div>
		</Teleport>

    <!-- import -->
    <Teleport to="body">
      <div v-if="showImport" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="showImport = false">
        <div class="card w-full max-w-xl p-6">
          <h3 class="mb-1 text-base font-semibold text-slate-100">导入账号 JSON</h3>
          <p class="mb-5 text-xs text-slate-500">支持 sub2api 导出与 CPA / Codex auth 格式。平台与目标分组不一致的条目会被跳过。</p>
          <div class="space-y-4">
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label">目标分组</label>
                <select v-model.number="imp.group_id" class="input">
                  <option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }} ({{ PLATFORM_LABELS[g.platform] }})</option>
                </select>
              </div>
              <div>
                <label class="label">格式</label>
                <select v-model="imp.format" class="input">
                  <option value="auto">自动识别</option>
                  <option value="sub2api">sub2api</option>
                  <option value="cpa">CPA / Codex</option>
                </select>
              </div>
            </div>
            <div>
              <label class="label">默认 Base URL(条目自带的优先)</label>
              <input v-model="imp.base_url" class="input font-mono" placeholder="留空用官方地址" />
            </div>
            <div>
              <label class="label">选择 JSON 文件或粘贴内容</label>
              <input ref="importFileInput" type="file" accept="application/json,.json" class="hidden" @change="readImportFile" />
              <div class="mb-2 flex items-center gap-2">
                <button type="button" class="btn-ghost !px-3 !py-1.5 text-xs" @click="chooseImportFile">选择 JSON 文件</button>
                <span v-if="importFileName" class="min-w-0 truncate text-xs text-signal-green" :title="importFileName">{{ importFileName }}</span>
                <button v-if="importFileName" type="button" class="text-xs text-slate-500 hover:text-slate-200" @click="clearImportFile">清除</button>
              </div>
              <textarea v-model="imp.data" rows="9" class="input font-mono text-xs" placeholder='{"accounts":[{"name":"...","platform":"anthropic","type":"oauth","credentials":{"access_token":"...","refresh_token":"..."}}]}' @change="matchImportGroup(imp.data, false)"></textarea>
              <p class="mt-1 text-xs text-slate-600">支持 sub2api、Codex auth.json、Claude Code credentials 与 CPA 导出；文件上限 850 KB。</p>
              <p v-if="detectedImportPlatforms.length === 1" class="mt-1 text-xs text-signal-cyan">已识别平台：{{ PLATFORM_LABELS[detectedImportPlatforms[0]] }}。请确认目标分组；系统会保留你的选择。</p>
              <p v-else-if="detectedImportPlatforms.length > 1" class="mt-1 text-xs text-amber">文件包含多个平台，请按目标分组分别导入；不匹配的条目会跳过。</p>
            </div>
            <label class="flex items-center gap-2 text-sm text-slate-400">
              <input v-model="imp.skip_expired" type="checkbox" class="accent-amber" /> 跳过已过期的 token
            </label>

            <div v-if="impResult" class="rounded-lg border border-white/10 bg-black/20 p-3 text-sm">
              <div class="text-slate-200">成功导入 <span class="text-signal-green font-semibold">{{ impResult.imported }}</span> 个,跳过 <span class="text-signal-red font-semibold">{{ impResult.skipped }}</span> 个。</div>
              <ul v-if="impResult.skipped_detail.length" class="mt-2 space-y-1 text-xs text-slate-500">
                <li v-for="(s, i) in impResult.skipped_detail" :key="i">跳过 {{ s.name || '(未命名)' }}:{{ s.reason }}</li>
              </ul>
            </div>

            <div class="flex justify-end gap-3 pt-2">
              <button class="btn-ghost" @click="showImport = false">关闭</button>
              <button class="btn-primary" :disabled="!imp.group_id || !imp.data" @click="doImport">开始导入</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
