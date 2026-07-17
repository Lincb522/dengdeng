<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, copyText, withToast } from '../api/client'
import type { ApiKey, Group } from '../api/types'
import { formatMoney, PLATFORM_LABELS } from '../api/types'
import { normalizeReasoningEffort, REASONING_OPTIONS, reasoningLabel } from '../api/reasoning'
import { useToast } from '../stores/toast'
import KeyQuickSetupModal from '../components/KeyQuickSetupModal.vue'

const toast = useToast()
const keys = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
const showCreate = ref(false)
const newName = ref('')
const newGroupID = ref<number | null>(null)
const newQuota = ref(0)
const newDailyQuota = ref(0)
const newReasoningEffort = ref('auto')
const createdPlain = ref('')
const createdKey = ref<ApiKey | null>(null)
const setupKey = ref<ApiKey | null>(null)
const setupPlain = ref('')
const showSetup = ref(false)
const settingKey = ref<ApiKey | null>(null)
const settingsForm = ref({ name: '', group_id: 0, reasoning_effort: 'auto', quota: 0, daily_quota: 0, status: 'active', rpm: 0, allowed_ips: '', blocked_ips: '', expires_at: '' })

const reasoningOptions = REASONING_OPTIONS

function groupPlatform(groupID: number | null | undefined) {
  return groups.value.find((group) => group.id === groupID)?.platform || ''
}
function quickSetupStorageKey(keyID: number) {
  return `dengdeng.quick-setup.key.${keyID}`
}

// The API server only stores a one-way hash of each secret. Keep a freshly
// created secret in this browser tab so a page refresh does not force a key
// rotation. sessionStorage is cleared when the tab is closed.
function rememberQuickSetupKey(key: ApiKey, plain: string) {
  if (!plain) return
  try {
    sessionStorage.setItem(quickSetupStorageKey(key.id), plain)
  } catch {
    // Privacy modes may deny storage; quick setup still works for this view.
  }
}

function rememberedQuickSetupKey(key: ApiKey) {
  try {
    return sessionStorage.getItem(quickSetupStorageKey(key.id)) || ''
  } catch {
    return ''
  }
}

function toMicro(value: number) { return Math.max(0, Math.round((Number(value) || 0) * 1_000_000)) }
function fromMicro(value: number) { return Number((Math.max(0, value || 0) / 1_000_000).toFixed(6)) }
function quotaLabel(value: number) { return value > 0 ? formatMoney(value) : '不设上限' }
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
  if (groups.value.length && newGroupID.value === null) {
    newGroupID.value = groups.value[0].id
  }
}
onMounted(load)

async function createKey() {
  if (!newName.value || !newGroupID.value) return
  const result = await withToast(
    () => api.post<{ key: ApiKey; plain: string }>('/api/user/keys', {
      name: newName.value,
      group_id: newGroupID.value,
	  reasoning_effort: groupPlatform(newGroupID.value) === 'openai' ? newReasoningEffort.value : 'auto',
      quota_micro: toMicro(newQuota.value),
      daily_quota_micro: toMicro(newDailyQuota.value),
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
	newReasoningEffort.value = 'auto'
    await load()
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
  await load()
}

function openSettings(key: ApiKey) {
  settingKey.value = key
  settingsForm.value = {
    name: key.name,
    group_id: key.group_id,
	  reasoning_effort: normalizeReasoningEffort(key.reasoning_effort),
    quota: fromMicro(key.quota_micro),
    daily_quota: fromMicro(key.daily_quota_micro),
    status: key.status,
    rpm: key.rpm || 0,
    allowed_ips: key.allowed_ips || '',
    blocked_ips: key.blocked_ips || '',
    expires_at: toLocalDateTime(key.expires_at),
  }
}

async function saveSettings() {
  if (!settingKey.value || !settingsForm.value.name || !settingsForm.value.group_id) return
  const saved = await withToast(() => api.put(`/api/user/keys/${settingKey.value!.id}`, {
    name: settingsForm.value.name,
    group_id: settingsForm.value.group_id,
	  reasoning_effort: groupPlatform(settingsForm.value.group_id) === 'openai' ? settingsForm.value.reasoning_effort : 'auto',
    quota_micro: toMicro(settingsForm.value.quota),
    daily_quota_micro: toMicro(settingsForm.value.daily_quota),
    status: settingsForm.value.status,
    rpm: Math.max(0, Math.floor(Number(settingsForm.value.rpm) || 0)),
    allowed_ips: settingsForm.value.allowed_ips,
    blocked_ips: settingsForm.value.blocked_ips,
    expires_at: settingsForm.value.expires_at ? new Date(settingsForm.value.expires_at).toISOString() : null,
  }), '密钥设置已保存')
  if (saved !== null) {
    settingKey.value = null
    await load()
  }
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

function onSetupEffortUpdated(value: string) {
  if (setupKey.value) setupKey.value = { ...setupKey.value, reasoning_effort: value }
  void load()
}
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>API 密钥</h1>
        <p class="mt-1 text-sm text-slate-500">密钥可独立绑定分组、总额度和每日额度；填 0 即不限制。</p>
      </div>
      <button class="btn-primary" @click="showCreate = true">新建密钥</button>
    </div>

    <div class="card overflow-x-auto">
      <table class="table-base">
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
            <td class="font-medium text-slate-200">{{ k.name }}</td>
            <td class="num text-xs text-slate-400">{{ k.key_preview }}</td>
            <td>
              <span class="tag-gray">{{ k.group?.name }}</span>
              <span class="ml-1.5 text-xs text-slate-500">{{ PLATFORM_LABELS[k.group?.platform || ''] }}</span>
            </td>
				<td class="text-xs">
					<div class="num text-slate-300">{{ k.quota_micro ? `${formatMoney(k.quota_used_micro)} / ${formatMoney(k.quota_micro)}` : '总额不限' }}</div>
					<div class="mt-1 text-slate-500">每日 {{ quotaLabel(k.daily_quota_micro) }}</div>
					<div v-if="k.group?.platform === 'openai'" class="mt-1 text-slate-500">思考强度 Reasoning Effort：{{ reasoningLabel(k.reasoning_effort) }}</div>
					<div v-if="k.rpm || k.expires_at || k.allowed_ips || k.blocked_ips" class="mt-1 text-slate-500">{{ k.rpm ? `${k.rpm} RPM` : '' }}{{ k.rpm && (k.expires_at || k.allowed_ips || k.blocked_ips) ? ' · ' : '' }}{{ k.expires_at ? `到期 ${new Date(k.expires_at).toLocaleDateString()}` : (k.allowed_ips || k.blocked_ips ? '已设 IP 规则' : '') }}</div>
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
    <Teleport to="body">
      <div v-if="showCreate" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="closeCreate">
        <div class="card w-full max-w-md p-6">
          <template v-if="!createdPlain">
            <h3 class="mb-5 text-base font-semibold text-slate-100">新建 API 密钥</h3>
            <div class="space-y-4">
              <div>
                <label class="label">密钥名称</label>
                <input v-model="newName" class="input" placeholder="例如:my-claude-code" maxlength="64" />
              </div>
              <div>
                <label class="label">选择分组</label>
                <select v-model="newGroupID" class="input">
                  <option v-for="g in groups" :key="g.id" :value="g.id">
                    {{ g.name }} ({{ PLATFORM_LABELS[g.platform] }}, 倍率 x{{ g.rate_multiplier }})
                  </option>
                </select>
                <p v-if="!groups.length" class="mt-2 text-xs text-signal-red">暂无开放分组,请联系管理员</p>
              </div>
					<div v-if="groupPlatform(newGroupID) === 'openai'" class="rounded-lg border border-amber/20 bg-amber/5 p-3">
						<label class="label">默认思考强度 Reasoning Effort</label>
						<select v-model="newReasoningEffort" class="input"><option v-for="option in reasoningOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select>
						<p class="mt-2 text-xs leading-5 text-slate-500">档位与 GPT‑5.6 官方一致；客户端显式设置时优先。高档位会按后台设置的独立倍率计费，创建后仍可修改。</p>
					</div>
				<div class="grid grid-cols-2 gap-3 rounded-lg border border-slate-800 bg-slate-950/35 p-3">
					<label><span class="label">总额度（USD）</span><input v-model.number="newQuota" type="number" min="0" step="0.01" class="input" placeholder="0 = 不限制" /></label>
					<label><span class="label">每日额度（USD）</span><input v-model.number="newDailyQuota" type="number" min="0" step="0.01" class="input" placeholder="0 = 不限制" /></label>
					<p class="col-span-2 text-xs leading-5 text-slate-500">额度按实际调用费用扣减，与账户余额独立；总额度耗尽或达到当日额度后，该密钥会被拒绝调用。</p>
				</div>
              <div class="flex justify-end gap-3 pt-2">
                <button class="btn-ghost" @click="closeCreate">取消</button>
                <button class="btn-primary" :disabled="!newName || !newGroupID" @click="createKey">创建</button>
              </div>
            </div>
          </template>
          <template v-else>
            <h3 class="mb-2 text-base font-semibold text-signal-green">密钥创建成功</h3>
            <p class="mb-4 text-xs text-slate-400">明文只显示这一次,请立即复制保存:</p>
            <button class="block w-full break-all rounded-lg border border-amber/30 bg-ink-950 p-4 text-left font-mono text-sm text-amber" title="点击复制密钥" @click="copyPlain">
              {{ createdPlain }}
            </button>
            <div class="mt-5 flex justify-end gap-3">
              <button class="btn-primary" @click="copyPlain">复制</button>
				<button class="btn-ghost" @click="openCreatedSetup">快速配置</button>
              <button class="btn-ghost" @click="closeCreate">完成</button>
            </div>
          </template>
        </div>
      </div>
    </Teleport>

		<Teleport to="body">
			<div v-if="settingKey" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="settingKey = null">
				<div class="card w-full max-w-md p-6">
					<h3 class="mb-1 text-base font-semibold text-slate-100">密钥设置</h3>
					<p class="mb-5 text-xs text-slate-500">预算按实际费用累计；设为 0 表示不设置该层上限。IP 规则和请求速率会在网关鉴权阶段执行。</p>
					<div class="space-y-4">
						<label><span class="label">密钥名称</span><input v-model.trim="settingsForm.name" class="input" maxlength="64" /></label>
						<label><span class="label">分组</span><select v-model.number="settingsForm.group_id" class="input"><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}（{{ PLATFORM_LABELS[group.platform] }}）</option></select></label>
						<div v-if="groupPlatform(settingsForm.group_id) === 'openai'" class="rounded-lg border border-amber/20 bg-amber/5 p-3"><label><span class="label">默认思考强度 Reasoning Effort</span><select v-model="settingsForm.reasoning_effort" class="input"><option v-for="option in reasoningOptions" :key="option.value" :value="option.value">{{ option.label }}</option></select></label><p class="mt-2 text-xs leading-5 text-slate-500">客户端显式设置时优先；费用按实际生效档位计算。</p></div>
						<div class="grid grid-cols-2 gap-3"><label><span class="label">总额度（USD）</span><input v-model.number="settingsForm.quota" type="number" min="0" step="0.01" class="input" /></label><label><span class="label">每日额度（USD）</span><input v-model.number="settingsForm.daily_quota" type="number" min="0" step="0.01" class="input" /></label></div>
						<div class="grid grid-cols-2 gap-3"><label><span class="label">每分钟请求数</span><input v-model.number="settingsForm.rpm" type="number" min="0" max="100000" step="1" class="input" placeholder="0 = 不限制" /></label><label><span class="label">到期时间</span><input v-model="settingsForm.expires_at" type="datetime-local" class="input" /><small class="mt-1 block text-[11px] text-slate-500">留空表示永久有效</small></label></div>
						<label><span class="label">IP 白名单</span><input v-model.trim="settingsForm.allowed_ips" class="input font-mono text-xs" placeholder="203.0.113.8, 2001:db8::/32（留空不限）" /><small class="mt-1 block text-[11px] text-slate-500">仅允许列出的 IP 或 CIDR；多个规则用逗号或空格分隔。</small></label>
						<label><span class="label">IP 黑名单</span><input v-model.trim="settingsForm.blocked_ips" class="input font-mono text-xs" placeholder="198.51.100.0/24（留空不拦截）" /><small class="mt-1 block text-[11px] text-slate-500">黑名单优先于白名单，用于立即阻断异常来源。</small></label>
						<label><span class="label">状态</span><select v-model="settingsForm.status" class="input"><option value="active">启用</option><option value="disabled">停用</option></select></label>
						<div class="flex justify-end gap-3 pt-2"><button class="btn-ghost" @click="settingKey = null">取消</button><button class="btn-primary" :disabled="!settingsForm.name || !settingsForm.group_id" @click="saveSettings">保存</button></div>
					</div>
				</div>
			</div>
		</Teleport>

		<KeyQuickSetupModal
			:show="showSetup"
			:api-key="setupPlain"
			:key-id="setupKey?.id || null"
			:key-name="setupKey?.name || ''"
			:platform="setupKey?.group?.platform || 'openai'"
			:reasoning-effort="setupKey?.reasoning_effort || 'auto'"
			@close="showSetup = false"
			@rotate="requestRotateForSetup"
			@effort-updated="onSetupEffortUpdated"
		/>
  </div>
</template>
