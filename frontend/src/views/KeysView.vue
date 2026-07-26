<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, copyText, withToast } from '../api/client'
import type { ApiKey, Group } from '../api/types'
import { formatMoney, PLATFORM_LABELS } from '../api/types'
import { normalizeReasoningEffort, REASONING_OPTIONS, reasoningLabel } from '../api/reasoning'
import { useToast } from '../stores/toast'
import { useAuth } from '../stores/auth'
import KeyQuickSetupModal from '../components/KeyQuickSetupModal.vue'
import AppModal from '../components/AppModal.vue'

const toast = useToast()
const auth = useAuth()
const keyMultiGroupEnabled = computed(() => auth.keyMultiGroupEnabled)
const customEndpoints = computed(() => auth.siteCustomization.custom_endpoints || [])
const keys = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
const showCreate = ref(false)
const creatingKey = ref(false)
const newName = ref('')
const newGroupIDs = ref<number[]>([])
const newQuota = ref(0)
const newDailyQuota = ref(0)
const newConcurrency = ref(0)
const newReasoningEffort = ref('auto')
const createdPlain = ref('')
const createdKey = ref<ApiKey | null>(null)
const setupKey = ref<ApiKey | null>(null)
const setupPlain = ref('')
const showSetup = ref(false)
const copiedKeyID = ref<number | null>(null)
const revealedKeyIDs = ref<Set<number>>(new Set())
const settingKey = ref<ApiKey | null>(null)
const savingSettings = ref(false)
const settingsGroupsOpen = ref(false)
const settingsQuotaOpen = ref(false)
const networkSecurityOpen = ref(false)
const settingsForm = ref({ name: '', group_ids: [] as number[], reasoning_effort: 'auto', quota: 0, daily_quota: 0, status: 'active', rpm: 0, concurrency: 0, allowed_ips: '', blocked_ips: '', expires_at: '' })

const reasoningOptions = REASONING_OPTIONS

function normalizeGroupSelection(groupIDs: number[]) {
  const unique = [...new Set(groupIDs.filter((id) => id > 0))]
  return keyMultiGroupEnabled.value ? unique : unique.slice(0, 1)
}

function selectNewGroup(groupID: number) {
  newGroupIDs.value = [groupID]
}

function selectSettingsGroup(groupID: number) {
  settingsForm.value.group_ids = [groupID]
}

function selectedGroups(groupIDs: number[]) {
  const selected = new Set(groupIDs)
  return groups.value.filter((group) => selected.has(group.id))
}

function selectedGroupSummary(groupIDs: number[]) {
  const names = selectedGroups(groupIDs).map((group) => group.name)
  if (!names.length) return '尚未选择分组'
  return `已选 ${names.length} 个 · ${names.join('、')}`
}

function keyGroups(key: ApiKey | null | undefined) {
  if (!key) return []
  if (Array.isArray(key.groups) && key.groups.length) return key.groups
  return key.group ? [key.group] : []
}

function keyGroupIDs(key: ApiKey) {
  if (Array.isArray(key.group_ids) && key.group_ids.length) return [...key.group_ids]
  const ids = keyGroups(key).map((group) => group.id)
  return ids.length ? ids : key.group_id ? [key.group_id] : []
}

function hasPlatform(groupIDs: number[], platform: string) {
  return selectedGroups(groupIDs).some((group) => group.platform === platform)
}

function keyHasPlatform(key: ApiKey, platform: string) {
  return keyGroups(key).some((group) => group.platform === platform)
}

function keyPlatforms(key: ApiKey | null | undefined) {
  return [...new Set(keyGroups(key).map((group) => group.platform))]
}

function quickSetupStorageKey(keyID: number) {
  return `dengdeng.quick-setup.key.${keyID}`
}

function matchesKeyPreview(key: ApiKey, plain: string) {
	const [prefix, suffix] = (key.key_preview || '').split('...')
	return !!plain && (!prefix || plain.startsWith(prefix)) && (!suffix || plain.endsWith(suffix))
}

// The API server only stores a one-way hash. Keep the secret in this browser's
// local storage so refreshes and browser restarts do not force a rotation.
function rememberQuickSetupKey(key: ApiKey, plain: string) {
  if (!plain) return
  try {
    localStorage.setItem(quickSetupStorageKey(key.id), plain)
	sessionStorage.removeItem(quickSetupStorageKey(key.id))
  } catch {
    // Privacy modes may deny storage; quick setup still works in memory.
  }
}

function rememberedQuickSetupKey(key: ApiKey) {
  try {
	const storageKey = quickSetupStorageKey(key.id)
	const persistent = localStorage.getItem(storageKey) || ''
	if (persistent) {
		if (matchesKeyPreview(key, persistent)) return persistent
		localStorage.removeItem(storageKey)
	}
	const legacy = sessionStorage.getItem(storageKey) || ''
	if (legacy && matchesKeyPreview(key, legacy)) {
		localStorage.setItem(storageKey, legacy)
		sessionStorage.removeItem(storageKey)
		return legacy
	}
	if (legacy) sessionStorage.removeItem(storageKey)
	return ''
  } catch {
    return ''
  }
}

function forgetQuickSetupKey(key: ApiKey) {
	try {
		localStorage.removeItem(quickSetupStorageKey(key.id))
		sessionStorage.removeItem(quickSetupStorageKey(key.id))
	} catch { /* storage is optional */ }
}

function plainForKey(key: ApiKey) {
  if (createdKey.value?.id === key.id && createdPlain.value) return createdPlain.value
  if (setupKey.value?.id === key.id && setupPlain.value) return setupPlain.value
  return rememberedQuickSetupKey(key)
}

async function copyKey(key: ApiKey) {
  const plain = plainForKey(key)
  if (!plain) {
    openQuickSetup(key)
    toast.show('旧密钥没有可读取的明文，请先补入原密钥', 'error')
    return
  }
  try {
    await copyText(plain)
    copiedKeyID.value = key.id
    toast.show('密钥已复制', 'success')
    window.setTimeout(() => {
      if (copiedKeyID.value === key.id) copiedKeyID.value = null
    }, 1600)
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '复制失败', 'error')
  }
}

function toMicro(value: number) { return Math.max(0, Math.round((Number(value) || 0) * 1_000_000)) }
function fromMicro(value: number) { return Number((Math.max(0, value || 0) / 1_000_000).toFixed(6)) }
function quotaLabel(value: number) { return value > 0 ? formatMoney(value) : '不设上限' }
function quotaTrafficSummary() {
  const total = settingsForm.value.quota > 0 ? `总额 $${settingsForm.value.quota}` : '总额不限'
  const daily = settingsForm.value.daily_quota > 0 ? `每日 $${settingsForm.value.daily_quota}` : '每日不限'
  const concurrency = settingsForm.value.concurrency > 0 ? `并发 ${settingsForm.value.concurrency}` : '并发不限'
  return `${total} · ${daily} · ${concurrency}`
}
function toLocalDateTime(value: string | null | undefined) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

async function load() {
  keys.value = await api.get<ApiKey[]>('/api/user/keys')
  groups.value = await api.get<Group[]>('/api/user/groups')
	newGroupIDs.value = normalizeGroupSelection(newGroupIDs.value)
  if (groups.value.length && !newGroupIDs.value.length) {
    newGroupIDs.value = [groups.value[0].id]
  }
}
onMounted(async () => {
	await auth.loadPublicSettings()
	await load()
})

async function createKey() {
  if (!newName.value || !newGroupIDs.value.length || creatingKey.value) return
  creatingKey.value = true
  try {
    const result = await withToast(
      () => api.post<{ key: ApiKey; plain: string }>('/api/user/keys', {
        name: newName.value,
		  group_ids: normalizeGroupSelection(newGroupIDs.value),
	    reasoning_effort: hasPlatform(newGroupIDs.value, 'openai') ? newReasoningEffort.value : 'auto',
        quota_micro: toMicro(newQuota.value),
        daily_quota_micro: toMicro(newDailyQuota.value),
		  concurrency: Math.max(0, Math.floor(Number(newConcurrency.value) || 0)),
      }),
      '密钥已创建',
    )
    if (result) {
      createdPlain.value = result.plain
		  createdKey.value = result.key
		  setupKey.value = result.key
		  setupPlain.value = result.plain
      rememberQuickSetupKey(result.key, result.plain)
      newName.value = ''
      newQuota.value = 0
      newDailyQuota.value = 0
		  newConcurrency.value = 0
	    newReasoningEffort.value = 'auto'
      await load()
    }
  } finally {
    creatingKey.value = false
  }
}

async function toggleKey(k: ApiKey) {
  const target = k.status === 'active' ? 'disabled' : 'active'
  await withToast(() => api.put(`/api/user/keys/${k.id}`, { status: target }), target === 'active' ? '已启用' : '已停用')
  await load()
}

async function removeKey(k: ApiKey) {
  if (!confirm(`确认删除密钥「${k.name}」?该操作不可恢复。`)) return
  await withToast(() => api.delete(`/api/user/keys/${k.id}`), '已删除')
	forgetQuickSetupKey(k)
  await load()
}

function toggleKeyReveal(key: ApiKey) {
	const next = new Set(revealedKeyIDs.value)
	if (next.has(key.id)) next.delete(key.id)
	else next.add(key.id)
	revealedKeyIDs.value = next
}

function openSettings(key: ApiKey) {
  settingKey.value = key
  settingsForm.value = {
    name: key.name,
	group_ids: normalizeGroupSelection(keyGroupIDs(key)),
	  reasoning_effort: normalizeReasoningEffort(key.reasoning_effort),
    quota: fromMicro(key.quota_micro),
    daily_quota: fromMicro(key.daily_quota_micro),
    status: key.status,
    rpm: key.rpm || 0,
		concurrency: key.concurrency || 0,
    allowed_ips: key.allowed_ips || '',
    blocked_ips: key.blocked_ips || '',
    expires_at: toLocalDateTime(key.expires_at),
  }
  settingsGroupsOpen.value = false
  settingsQuotaOpen.value = false
  networkSecurityOpen.value = !!(key.allowed_ips || key.blocked_ips)
}

async function saveSettings() {
  if (!settingKey.value || !settingsForm.value.name || !settingsForm.value.group_ids.length || savingSettings.value) return
  savingSettings.value = true
  try {
    const saved = await withToast(() => api.put(`/api/user/keys/${settingKey.value!.id}`, {
      name: settingsForm.value.name,
		  group_ids: normalizeGroupSelection(settingsForm.value.group_ids),
	    reasoning_effort: hasPlatform(settingsForm.value.group_ids, 'openai') ? settingsForm.value.reasoning_effort : 'auto',
      quota_micro: toMicro(settingsForm.value.quota),
      daily_quota_micro: toMicro(settingsForm.value.daily_quota),
      status: settingsForm.value.status,
      rpm: Math.max(0, Math.floor(Number(settingsForm.value.rpm) || 0)),
		  concurrency: Math.max(0, Math.floor(Number(settingsForm.value.concurrency) || 0)),
      allowed_ips: settingsForm.value.allowed_ips,
      blocked_ips: settingsForm.value.blocked_ips,
      expires_at: settingsForm.value.expires_at ? new Date(settingsForm.value.expires_at).toISOString() : null,
    }), '密钥设置已保存')
    if (saved !== null) {
      settingKey.value = null
      await load()
    }
  } finally {
    savingSettings.value = false
  }
}

function closeSettings() {
  if (!savingSettings.value) settingKey.value = null
}

async function copyPlain() {
  try {
    await copyText(createdPlain.value)
    toast.show('已复制到剪贴板', 'success')
  } catch (error) {
    toast.show(error instanceof Error ? error.message : '复制失败', 'error')
  }
}

function openQuickSetup(key: ApiKey) {
	const isCurrentKey = setupKey.value?.id === key.id
	const currentPlain = setupPlain.value
	setupKey.value = key
	setupPlain.value = isCurrentKey && currentPlain
		? currentPlain
		: rememberedQuickSetupKey(key)
	showSetup.value = true
}

function closeQuickSetup() {
  showSetup.value = false
  if (setupKey.value) setupPlain.value = rememberedQuickSetupKey(setupKey.value)
}

function requestRotateForSetup() {
	const key = setupKey.value
	if (!key) return
	if (!confirm(`重新生成「${key.name}」会让当前密钥立即失效。确认继续吗？`)) return
	void rotateForSetup(key)
}

async function rotateForSetup(key: ApiKey) {
	const result = await withToast(
		() => api.post<{ key: ApiKey; plain: string }>(`/api/user/keys/${key.id}/rotate`, {}),
		'已生成新密钥，旧密钥已失效',
	)
	if (!result) return
	setupKey.value = result.key
	setupPlain.value = result.plain
	createdKey.value = result.key
	createdPlain.value = result.plain
	rememberQuickSetupKey(result.key, result.plain)
	showSetup.value = true
	await load()
}

function openCreatedSetup() {
  if (!createdKey.value || !createdPlain.value) return
  setupKey.value = createdKey.value
  setupPlain.value = createdPlain.value
	showCreate.value = false
	showSetup.value = true
}

function closeCreate() {
  showCreate.value = false
  createdPlain.value = ''
	createdKey.value = null
}

// 在快速配置弹窗里改了默认思考强度后，同步列表数据（不打断弹窗）。
function onSetupEffortUpdated(value: string) {
  if (setupKey.value) setupKey.value = { ...setupKey.value, reasoning_effort: value }
  void load()
}

function onSetupSecretForgot() {
	if (!setupKey.value) return
	forgetQuickSetupKey(setupKey.value)
	setupPlain.value = ''
	if (createdKey.value?.id === setupKey.value.id) {
		createdPlain.value = ''
		createdKey.value = null
	}
	const next = new Set(revealedKeyIDs.value)
	next.delete(setupKey.value.id)
	revealedKeyIDs.value = next
}
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>API 密钥</h1>
		<p class="mt-1 text-sm text-slate-500">{{ keyMultiGroupEnabled ? '一把密钥可绑定多个分组' : '每把密钥绑定一个分组' }}，并可独立设置总额度和每日额度；填 0 即不限制。</p>
      </div>
      <button class="btn-primary" @click="showCreate = true">新建密钥</button>
    </div>

    <div class="card overflow-x-auto">
      <table v-responsive-table class="table-base key-table">
        <colgroup>
          <col class="key-col-name" />
          <col class="key-col-secret" />
          <col class="key-col-groups" />
          <col class="key-col-quota" />
          <col class="key-col-status" />
          <col class="key-col-used" />
          <col class="key-col-actions" />
        </colgroup>
        <thead>
          <tr>
            <th>名称</th>
            <th>密钥</th>
            <th>分组</th>
				<th>额度</th>
            <th>状态</th>
            <th>最后使用</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="k in keys" :key="k.id">
            <td class="whitespace-nowrap font-medium text-slate-200">{{ k.name }}</td>
            <td>
              <div class="key-secret-cell">
				<code class="num" :class="{ 'is-revealed': revealedKeyIDs.has(k.id) && plainForKey(k) }">{{ revealedKeyIDs.has(k.id) && plainForKey(k) ? plainForKey(k) : k.key_preview }}</code>
				<span v-if="plainForKey(k)" class="key-secret-saved">本机已保存</span>
				<button v-if="plainForKey(k)" class="btn-ghost" @click="toggleKeyReveal(k)">{{ revealedKeyIDs.has(k.id) ? '隐藏' : '显示' }}</button>
                <button
				  class="btn-ghost"
                  :title="plainForKey(k) ? '复制完整密钥' : '补入原密钥后即可复制'"
                  @click="copyKey(k)"
                >
                  {{ plainForKey(k) ? (copiedKeyID === k.id ? '已复制' : '复制') : '补入' }}
                </button>
              </div>
            </td>
            <td>
				<div class="key-group-tags">
					<span v-for="group in keyGroups(k)" :key="group.id" class="tag-gray group-tag">{{ group.name }} · {{ PLATFORM_LABELS[group.platform] }}</span>
					<span v-if="!keyGroups(k).length" class="text-xs text-slate-500">未绑定</span>
				</div>
            </td>
				<td class="text-xs">
					<div class="num text-slate-300">{{ k.quota_micro ? `${formatMoney(k.quota_used_micro)} / ${formatMoney(k.quota_micro)}` : '总额不限' }}</div>
					<div class="mt-1 text-slate-500">每日 {{ quotaLabel(k.daily_quota_micro) }}</div>
					<div v-if="keyHasPlatform(k, 'openai')" class="mt-1 text-slate-500">思考强度 Reasoning Effort：{{ reasoningLabel(k.reasoning_effort) }}</div>
					<div v-if="k.rpm || k.concurrency || k.expires_at || k.allowed_ips || k.blocked_ips" class="mt-1 text-slate-500">{{ k.rpm ? `${k.rpm} RPM · ` : '' }}{{ k.concurrency ? `并发 ${k.concurrency}` : '并发不限' }}{{ k.expires_at ? ` · 到期 ${new Date(k.expires_at).toLocaleDateString()}` : (k.allowed_ips || k.blocked_ips ? ' · 已设 IP 规则' : '') }}</div>
				</td>
            <td>
              <span :class="k.status === 'active' ? 'tag-green' : 'tag-red'">
                {{ k.status === 'active' ? '启用' : '停用' }}
              </span>
            </td>
            <td class="text-xs text-slate-500">{{ k.last_used_at ? new Date(k.last_used_at).toLocaleString() : '从未' }}</td>
            <td class="text-right">
              <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="toggleKey(k)">
                {{ k.status === 'active' ? '停用' : '启用' }}
              </button>
				<button class="btn-ghost ml-2 !px-2.5 !py-1 text-xs" @click="openSettings(k)">设置</button>
				<button class="btn-ghost ml-2 !px-2.5 !py-1 text-xs" @click="openQuickSetup(k)">快速配置</button>
              <button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="removeKey(k)">删除</button>
            </td>
          </tr>
          <tr v-if="!keys.length">
            <td colspan="7" class="py-10 text-center text-sm text-slate-500">还没有密钥,点击右上角「新建密钥」开始使用</td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 创建弹窗 -->
	<section v-if="customEndpoints.length" class="card mt-6 p-5">
		<h2 class="text-sm font-semibold text-slate-200">其他端点</h2>
		<div class="mt-3 grid gap-2 sm:grid-cols-2">
			<article v-for="endpoint in customEndpoints" :key="endpoint.id" class="rounded-lg border border-slate-800 p-3">
				<div class="flex items-center justify-between gap-3"><strong class="text-xs text-slate-200">{{ endpoint.name }}</strong><button type="button" class="btn-ghost !px-2 !py-1 text-[11px]" @click="copyText(endpoint.url)">复制</button></div>
				<code class="mt-2 block overflow-hidden text-ellipsis whitespace-nowrap text-[11px] text-amber" :title="endpoint.url">{{ endpoint.url }}</code>
				<p v-if="endpoint.description" class="mt-2 text-xs text-slate-500">{{ endpoint.description }}</p>
			</article>
		</div>
	</section>

    <AppModal
      :open="showCreate"
      :title="createdPlain ? '密钥创建成功' : '新建 API 密钥'"
      :description="createdPlain ? '明文只显示一次，请立即复制并妥善保存。' : '选择可用分组并为这把密钥设置独立限制。'"
      width="standard"
      :busy="creatingKey"
      initial-focus="input"
      @close="closeCreate"
    >
      <div v-if="!createdPlain" class="modal-form">
        <section class="modal-section">
          <label class="modal-field"><span class="label">密钥名称</span><input v-model.trim="newName" class="input" placeholder="例如：my-claude-code" maxlength="64" /></label>
          <div class="modal-field">
            <span class="label">{{ keyMultiGroupEnabled ? '选择分组（可多选）' : '选择分组' }}</span>
            <div v-if="groups.length" class="key-group-picker key-group-picker--create" role="group" aria-label="选择密钥分组">
              <label v-for="g in groups" :key="g.id" :class="{ 'is-selected': newGroupIDs.includes(g.id) }">
                <input v-if="keyMultiGroupEnabled" v-model="newGroupIDs" type="checkbox" :value="g.id" />
                <input v-else type="radio" name="new-key-group" :checked="newGroupIDs[0] === g.id" @change="selectNewGroup(g.id)" />
                <span><strong>{{ g.name }}</strong><small>{{ PLATFORM_LABELS[g.platform] }} · 倍率 ×{{ g.rate_multiplier }}</small></span>
              </label>
            </div>
            <p v-else class="modal-note">当前没有可用分组，请联系管理员开放分组后再创建密钥。</p>
            <p v-if="newGroupIDs.length && keyMultiGroupEnabled" class="key-group-picker-note">已选择 {{ newGroupIDs.length }} 个分组；同平台不可用时会自动切换。</p>
          </div>
          <label v-if="hasPlatform(newGroupIDs, 'openai')" class="modal-field"><span class="label">默认思考强度</span><select v-model="newReasoningEffort" class="input"><option v-for="option in reasoningOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select><small class="modal-field__hint">客户端显式设置优先，费用按实际生效档位计算。</small></label>
        </section>
        <section class="modal-section">
          <div class="modal-section__head"><strong>使用限制</strong><p>0 表示不限制；额度按实际调用费用累计。</p></div>
          <div class="modal-grid modal-grid--two">
            <label class="modal-field"><span class="label">总额度（USD）</span><input v-model.number="newQuota" type="number" min="0" step="0.01" class="input" placeholder="0 = 不限制" /></label>
            <label class="modal-field"><span class="label">每日额度（USD）</span><input v-model.number="newDailyQuota" type="number" min="0" step="0.01" class="input" placeholder="0 = 不限制" /></label>
          </div>
          <label class="modal-field"><span class="label">并发上限</span><input v-model.number="newConcurrency" type="number" min="0" max="10000" step="1" class="input" placeholder="0 = 不限制" /></label>
        </section>
      </div>
      <section v-else class="key-created-state" aria-live="polite">
        <span class="key-created-state__mark" aria-hidden="true">✓</span>
        <div><strong>可以开始使用</strong><p>点击下方密钥即可复制。离开后服务端无法再次显示明文。</p></div>
        <button type="button" class="key-created-secret" title="点击复制密钥" @click="copyPlain"><code>{{ createdPlain }}</code><span>复制</span></button>
      </section>
      <template #footer>
        <template v-if="!createdPlain">
          <button type="button" class="btn-ghost" :disabled="creatingKey" @click="closeCreate">取消</button>
          <button type="button" class="btn-primary" :disabled="creatingKey || !newName || !newGroupIDs.length" @click="createKey">{{ creatingKey ? '创建中…' : '创建密钥' }}</button>
        </template>
        <template v-else>
          <button type="button" class="btn-ghost" @click="closeCreate">完成</button>
          <button type="button" class="btn-ghost" @click="openCreatedSetup">快速配置</button>
          <button type="button" class="btn-primary" @click="copyPlain">复制密钥</button>
        </template>
      </template>
    </AppModal>

		<AppModal
			:open="!!settingKey"
			title="API 密钥设置"
			description="修改会立即作用于后续请求，不需要重新生成密钥。"
			width="wide"
			:busy="savingSettings"
			@close="closeSettings"
		>
			<template #header-meta><div v-if="settingKey" class="key-settings-meta"><code>{{ settingKey.key_preview }}</code><span :class="settingKey.status === 'active' ? 'is-active' : 'is-disabled'">{{ settingKey.status === 'active' ? '已启用' : '已停用' }}</span></div></template>
			<div class="modal-form">
				<section class="modal-section">
					<div class="modal-section__head"><strong>基本设置</strong></div>
					<label class="modal-field"><span class="label">密钥名称</span><input v-model.trim="settingsForm.name" class="input" maxlength="64" /></label>
					<details class="modal-disclosure key-group-disclosure" :open="settingsGroupsOpen" @toggle="settingsGroupsOpen = ($event.currentTarget as HTMLDetailsElement).open">
						<summary><span><strong>{{ keyMultiGroupEnabled ? '分组（可多选）' : '分组' }}</strong><small :title="selectedGroupSummary(settingsForm.group_ids)">{{ selectedGroupSummary(settingsForm.group_ids) }}</small></span></summary>
						<div class="modal-disclosure__body"><div class="key-group-picker key-group-picker--settings" role="group" aria-label="编辑密钥分组"><label v-for="group in groups" :key="group.id" :class="{ 'is-selected': settingsForm.group_ids.includes(group.id) }"><input v-if="keyMultiGroupEnabled" v-model="settingsForm.group_ids" type="checkbox" :value="group.id" /><input v-else type="radio" name="edit-key-group" :checked="settingsForm.group_ids[0] === group.id" @change="selectSettingsGroup(group.id)" /><span><strong>{{ group.name }}</strong><small>{{ PLATFORM_LABELS[group.platform] }} · 倍率 ×{{ group.rate_multiplier }}</small></span></label></div></div>
					</details>
					<label v-if="hasPlatform(settingsForm.group_ids, 'openai')" class="modal-field"><span class="label">默认思考强度</span><select v-model="settingsForm.reasoning_effort" class="input"><option v-for="option in reasoningOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></label>
					<label class="modal-switch-row"><span><strong>启用密钥</strong><small>停用后所有新请求都会被拒绝。</small></span><input type="checkbox" :checked="settingsForm.status === 'active'" @change="settingsForm.status = ($event.target as HTMLInputElement).checked ? 'active' : 'disabled'" /></label>
				</section>
				<details class="modal-disclosure key-settings-disclosure" :open="settingsQuotaOpen" @toggle="settingsQuotaOpen = ($event.currentTarget as HTMLDetailsElement).open">
					<summary><span><strong>配额与流量</strong><small :title="quotaTrafficSummary()">{{ quotaTrafficSummary() }}</small></span></summary>
					<div class="modal-disclosure__body"><p class="key-settings-disclosure__note">0 表示不限制，预算按实际费用累计。</p><div class="modal-grid modal-grid--two"><label class="modal-field"><span class="label">总额度（USD）</span><input v-model.number="settingsForm.quota" type="number" min="0" step="0.01" class="input" /></label><label class="modal-field"><span class="label">每日额度（USD）</span><input v-model.number="settingsForm.daily_quota" type="number" min="0" step="0.01" class="input" /></label><label class="modal-field"><span class="label">每分钟请求数</span><input v-model.number="settingsForm.rpm" type="number" min="0" max="100000" step="1" class="input" placeholder="0 = 不限制" /></label><label class="modal-field"><span class="label">并发上限</span><input v-model.number="settingsForm.concurrency" type="number" min="0" max="10000" step="1" class="input" placeholder="0 = 不限制" /></label></div><label class="modal-field"><span class="label">到期时间</span><input v-model="settingsForm.expires_at" type="datetime-local" class="input" /><small class="modal-field__hint">留空表示永久有效。</small></label></div>
				</details>
				<details class="modal-disclosure" :open="networkSecurityOpen" @toggle="networkSecurityOpen = ($event.currentTarget as HTMLDetailsElement).open">
					<summary><span><strong>网络安全</strong><small>{{ settingsForm.allowed_ips || settingsForm.blocked_ips ? '已配置 IP 规则' : '未配置' }}</small></span></summary>
					<div class="modal-disclosure__body"><label class="modal-field"><span class="label">IP 白名单</span><input v-model.trim="settingsForm.allowed_ips" class="input font-mono text-xs" placeholder="203.0.113.8, 2001:db8::/32" /><small class="modal-field__hint">仅允许列出的 IP 或 CIDR，多个规则用逗号或空格分隔。</small></label><label class="modal-field"><span class="label">IP 黑名单</span><input v-model.trim="settingsForm.blocked_ips" class="input font-mono text-xs" placeholder="198.51.100.0/24" /><small class="modal-field__hint">黑名单优先于白名单，用于立即阻断异常来源。</small></label></div>
				</details>
			</div>
			<template #footer><button type="button" class="btn-ghost" :disabled="savingSettings" @click="closeSettings">取消</button><button type="button" class="btn-primary" :disabled="savingSettings || !settingsForm.name || !settingsForm.group_ids.length" @click="saveSettings">{{ savingSettings ? '保存中…' : '保存设置' }}</button></template>
		</AppModal>

    <KeyQuickSetupModal
      :show="showSetup"
			:api-key="setupPlain"
			:key-id="setupKey?.id || null"
			:key-name="setupKey?.name || ''"
			:key-preview="setupKey?.key_preview || ''"
			:platform="keyGroups(setupKey)[0]?.platform || 'openai'"
			:platforms="keyPlatforms(setupKey)"
			:reasoning-effort="setupKey?.reasoning_effort || 'auto'"
      @close="closeQuickSetup"
			@rotate="requestRotateForSetup"
			@effort-updated="onSetupEffortUpdated"
			@forget="onSetupSecretForgot"
		/>
  </div>
</template>
