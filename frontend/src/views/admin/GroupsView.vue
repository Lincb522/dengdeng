<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { Group } from '../../api/types'
import { PLATFORM_LABELS } from '../../api/types'
import AppModal from '../../components/AppModal.vue'

const groups = ref<Group[]>([])
const showForm = ref(false)
const editing = ref<Group | null>(null)
const saving = ref(false)
const cacheOpen = ref(false)
const advancedOpen = ref(false)
const deletingID = ref<number | null>(null)
const deleteGroup = ref<Group | null>(null)
const deleteDependencies = ref<GroupDependenciesResponse | null>(null)
const deleteTargetGroupID = ref(0)
const reasoningEfforts = ['none', 'low', 'medium', 'high', 'xhigh', 'max'] as const

type DeleteGroupResult = {
  deleted: boolean
  deleted_keys: number
  retained_keys: number
  deleted_accounts: number
  retained_accounts: number
  target_group_id: number
}

type GroupDependencySummary = {
  api_keys: number
  shared_api_keys: number
  exclusive_api_keys: number
  accounts: number
  shared_accounts: number
  exclusive_accounts: number
  subscriptions: number
  user_rates: number
  image_models: number
  alert_rules: number
}

type GroupDependenciesResponse = {
  group: Group
  dependencies: GroupDependencySummary
  total_dependencies: number
  can_delete_directly: boolean
  target_groups: Group[]
}

const form = ref({
  name: '',
  platform: 'anthropic' as string,
  description: '',
  rate_multiplier: 1,
	cache_read_multiplier: 1,
	cache_write_5m_multiplier: 1,
	cache_write_1h_multiplier: 1,
	image_rate_independent: false,
	image_rate_multiplier: 1,
	max_reasoning_effort: 'auto',
	reasoning_effort_mappings: {} as Record<string, string>,
  is_public: true,
  status: 'active',
})

async function load() {
  groups.value = await api.get<Group[]>('/api/admin/groups')
}
onMounted(load)

function openCreate() {
  editing.value = null
  form.value = {
    name: '', platform: 'anthropic', description: '', rate_multiplier: 1,
    cache_read_multiplier: 1, cache_write_5m_multiplier: 1, cache_write_1h_multiplier: 1,
    image_rate_independent: false, image_rate_multiplier: 1,
		max_reasoning_effort: 'auto', reasoning_effort_mappings: {} as Record<string, string>,
    is_public: true, status: 'active',
  }
  cacheOpen.value = false
  advancedOpen.value = false
  showForm.value = true
}

function openEdit(g: Group) {
  editing.value = g
  form.value = {
    name: g.name,
    platform: g.platform,
    description: g.description,
    rate_multiplier: g.rate_multiplier,
		cache_read_multiplier: g.cache_read_multiplier || 1,
		cache_write_5m_multiplier: g.cache_write_5m_multiplier || 1,
		cache_write_1h_multiplier: g.cache_write_1h_multiplier || 1,
		image_rate_independent: g.image_rate_independent || false,
		image_rate_multiplier: g.image_rate_multiplier || 1,
		max_reasoning_effort: g.max_reasoning_effort || 'auto',
		reasoning_effort_mappings: { ...(g.reasoning_effort_mappings || {}) },
    is_public: g.is_public,
    status: g.status,
  }
  cacheOpen.value = [g.cache_read_multiplier, g.cache_write_5m_multiplier, g.cache_write_1h_multiplier]
    .some((value) => Number(value || 1) !== 1)
  advancedOpen.value = !!g.image_rate_independent
    || (g.max_reasoning_effort || 'auto') !== 'auto'
    || Object.keys(g.reasoning_effort_mappings || {}).length > 0
  showForm.value = true
}

function closeForm() {
  if (saving.value) return
  showForm.value = false
}

function updateDisclosure(target: 'cache' | 'advanced', event: Event) {
  const open = (event.currentTarget as HTMLDetailsElement).open
  if (target === 'cache') cacheOpen.value = open
  else advancedOpen.value = open
}

async function save() {
  if (saving.value) return
  saving.value = true
  const body = {
    ...form.value,
    rate_multiplier: Number(form.value.rate_multiplier),
    cache_read_multiplier: Number(form.value.cache_read_multiplier),
    cache_write_5m_multiplier: Number(form.value.cache_write_5m_multiplier),
    cache_write_1h_multiplier: Number(form.value.cache_write_1h_multiplier),
    image_rate_multiplier: Number(form.value.image_rate_multiplier),
		reasoning_effort_mappings: Object.fromEntries(
			Object.entries(form.value.reasoning_effort_mappings).filter(([source, target]) => target && source !== target),
		),
  }
  try {
    const ok = editing.value
      ? await withToast(() => api.put(`/api/admin/groups/${editing.value!.id}`, body), '已保存')
      : await withToast(() => api.post('/api/admin/groups', body), '分组已创建')
    if (ok !== null) {
      showForm.value = false
      await load()
    }
  } finally {
    saving.value = false
  }
}

async function remove(g: Group) {
  if (deletingID.value !== null) return
  deletingID.value = g.id
  try {
    const dependencies = await withToast(() => api.get<GroupDependenciesResponse>(`/api/admin/groups/${g.id}/dependencies`))
    if (!dependencies) return
    if (dependencies.can_delete_directly) {
      if (!confirm(`确认删除空分组「${g.name}」？`)) return
      const deleted = await withToast(() => api.delete<DeleteGroupResult>(`/api/admin/groups/${g.id}`), '分组已删除')
      if (deleted !== null) await load()
      return
    }
    deleteGroup.value = g
    deleteDependencies.value = dependencies
    deleteTargetGroupID.value = dependencies.target_groups.find((item) => item.status === 'active')?.id || 0
  } finally {
    deletingID.value = null
  }
}

function closeDeleteDialog() {
  if (deletingID.value !== null) return
  deleteGroup.value = null
  deleteDependencies.value = null
  deleteTargetGroupID.value = 0
}

async function confirmUnbindAndDelete() {
  const group = deleteGroup.value
  if (!group || deletingID.value !== null) return
  deletingID.value = group.id
  let deleted: DeleteGroupResult | null = null
  try {
    const target = deleteTargetGroupID.value > 0 ? `&target_group_id=${deleteTargetGroupID.value}` : ''
    deleted = await withToast(
      () => api.delete<DeleteGroupResult>(`/api/admin/groups/${group.id}?force=true${target}`),
      deleteTargetGroupID.value > 0 ? '关联资源已迁移，原分组已删除' : '绑定已解除，分组已删除',
    )
  } finally {
    deletingID.value = null
  }
  if (deleted !== null) {
    closeDeleteDialog()
    await load()
  }
}

async function togglePublic(g: Group) {
  const target = !g.is_public
  const label = target ? '已对普通用户开放' : '已设为私有分组'
  const saved = await withToast(() => api.put(`/api/admin/groups/${g.id}`, { is_public: target }), label)
  if (saved !== null) await load()
}
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>分组管理</h1>
      </div>
      <button class="btn-primary" @click="openCreate">新建分组</button>
    </div>

    <div class="card overflow-x-auto">
      <table v-responsive-table class="table-base">
        <thead>
          <tr>
            <th>名称</th>
            <th>平台</th>
            <th>账号 (健康/总数)</th>
				<th>计费</th>
            <th>开放</th>
            <th>状态</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="g in groups" :key="g.id">
            <td>
              <div class="font-medium text-slate-200">{{ g.name }}</div>
              <div class="text-xs text-slate-500">{{ g.description }}</div>
            </td>
            <td><span class="tag-amber">{{ PLATFORM_LABELS[g.platform] }}</span></td>
            <td class="num text-sm">
              <span :class="(g.account_alive ?? 0) > 0 ? 'text-signal-green' : 'text-signal-red'">{{ g.account_alive ?? 0 }}</span>
              <span class="text-slate-500"> / {{ g.account_total ?? 0 }}</span>
            </td>
				<td>
					<div class="num text-sm">基础 x{{ g.rate_multiplier }}</div>
					<div class="mt-1 text-xs text-slate-500">命中 x{{ g.cache_read_multiplier || 1 }} · 5m x{{ g.cache_write_5m_multiplier || 1 }} · 1h x{{ g.cache_write_1h_multiplier || 1 }}</div>
					<div v-if="g.image_rate_independent" class="mt-1 text-xs text-amber">图像 x{{ g.image_rate_multiplier || 1 }}</div>
					<div v-if="g.platform === 'openai' || g.platform === 'grok'" class="mt-1 text-xs text-slate-500">思考上限 {{ g.max_reasoning_effort && g.max_reasoning_effort !== 'auto' ? g.max_reasoning_effort : '不限制' }}</div>
				</td>
            <td>
              <span :class="g.is_public ? 'tag-green' : 'tag-gray'">{{ g.is_public ? '公开' : '私有' }}</span>
            </td>
            <td>
              <span :class="g.status === 'active' ? 'tag-green' : 'tag-red'">{{ g.status === 'active' ? '启用' : '停用' }}</span>
            </td>
            <td class="text-right">
              <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="togglePublic(g)">{{ g.is_public ? '设为私有' : '对外开放' }}</button>
              <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="openEdit(g)">编辑</button>
              <button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" :disabled="deletingID !== null" @click="remove(g)">{{ deletingID === g.id ? '删除中…' : '删除' }}</button>
            </td>
          </tr>
          <tr v-if="!groups.length">
            <td colspan="7" class="py-10 text-center text-sm text-slate-500">暂无分组</td>
          </tr>
        </tbody>
      </table>
    </div>

    <AppModal
      :open="showForm"
      :title="editing ? '编辑分组' : '新建分组'"
      width="wide"
      :busy="saving"
      initial-focus="input"
      @close="closeForm"
    >
      <div class="modal-form">
        <section class="modal-section">
          <div class="modal-section__head"><strong>基本信息</strong></div>
          <label class="modal-field"><span class="label">名称</span><input v-model.trim="form.name" class="input" placeholder="例如：claude-standard" /></label>
          <div class="modal-grid modal-grid--two">
            <label class="modal-field">
              <span class="label">平台</span>
              <select v-model="form.platform" class="input" :disabled="!!editing">
                <option value="anthropic">Claude (Anthropic)</option><option value="openai">OpenAI</option><option value="gemini">Gemini</option><option value="grok">Grok (xAI)</option>
              </select>
              <small v-if="editing" class="modal-field__hint">已有账号依赖当前平台，编辑时不可更换。</small>
            </label>
            <label class="modal-field"><span class="label">基础倍率</span><input v-model.number="form.rate_multiplier" type="number" step="0.1" min="0.1" class="input" /></label>
          </div>
          <label class="modal-field"><span class="label">描述</span><input v-model.trim="form.description" class="input" placeholder="可选" /></label>
          <div>
            <label class="modal-switch-row"><span><strong>启用分组</strong><small>停用后不再参与新请求调度。</small></span><input type="checkbox" :checked="form.status === 'active'" @change="form.status = ($event.target as HTMLInputElement).checked ? 'active' : 'disabled'" /></label>
            <label class="modal-switch-row"><span><strong>对普通用户开放</strong><small>允许用户自行选择此分组创建密钥。</small></span><input v-model="form.is_public" type="checkbox" /></label>
          </div>
        </section>

        <details class="modal-disclosure" :open="cacheOpen" @toggle="updateDisclosure('cache', $event)">
          <summary><span><strong>缓存倍率</strong><small>命中 ×{{ form.cache_read_multiplier }} · 5m ×{{ form.cache_write_5m_multiplier }} · 1h ×{{ form.cache_write_1h_multiplier }}</small></span></summary>
          <div class="modal-disclosure__body">
            <p class="modal-field__hint">倍率在基础倍率之上叠加；没有 TTL 明细的旧响应按 5m 规则处理。</p>
            <div class="modal-grid modal-grid--three">
              <label class="modal-field"><span class="label">缓存命中</span><input v-model.number="form.cache_read_multiplier" type="number" step="0.1" min="0.1" class="input" /></label>
              <label class="modal-field"><span class="label">短缓存 5m</span><input v-model.number="form.cache_write_5m_multiplier" type="number" step="0.1" min="0.1" class="input" /></label>
              <label class="modal-field"><span class="label">长缓存 1h</span><input v-model.number="form.cache_write_1h_multiplier" type="number" step="0.1" min="0.1" class="input" /></label>
            </div>
          </div>
        </details>

        <details class="modal-disclosure" :open="advancedOpen" @toggle="updateDisclosure('advanced', $event)">
          <summary><span><strong>高级计费策略</strong><small>{{ form.image_rate_independent ? `图像 ×${form.image_rate_multiplier}` : '图像继承基础倍率' }}{{ form.platform === 'openai' || form.platform === 'grok' ? ` · 思考上限 ${form.max_reasoning_effort === 'auto' ? '不限' : form.max_reasoning_effort}` : '' }}</small></span></summary>
          <div class="modal-disclosure__body">
            <label class="modal-switch-row"><span><strong>图像独立倍率</strong><small>单独计费的图像 token 不继承基础倍率。</small></span><input v-model="form.image_rate_independent" type="checkbox" /></label>
            <label v-if="form.image_rate_independent" class="modal-field"><span class="label">图像倍率</span><input v-model.number="form.image_rate_multiplier" type="number" step="0.1" min="0.1" class="input" /></label>
            <template v-if="form.platform === 'openai' || form.platform === 'grok'">
              <label class="modal-field"><span class="label">思考强度上限</span><select v-model="form.max_reasoning_effort" class="input"><option value="auto">不限制</option><option v-for="effort in reasoningEfforts" :key="effort" :value="effort">{{ effort }}</option></select></label>
              <div class="modal-section__head"><strong>Reasoning Effort 映射</strong><p>客户端档位先映射，再按上限限制。</p></div>
              <div class="modal-grid modal-grid--three">
                <label v-for="effort in reasoningEfforts" :key="effort" class="modal-field"><span class="label font-mono">{{ effort }} →</span><select v-model="form.reasoning_effort_mappings[effort]" class="input"><option value="">保持原值</option><option v-for="target in reasoningEfforts" :key="target" :value="target">{{ target }}</option></select></label>
              </div>
            </template>
          </div>
        </details>
      </div>
      <template #footer>
        <button type="button" class="btn-ghost" :disabled="saving" @click="closeForm">取消</button>
        <button type="button" class="btn-primary" :disabled="saving || !form.name" @click="save">{{ saving ? '保存中…' : (editing ? '保存修改' : '创建分组') }}</button>
      </template>
    </AppModal>

    <AppModal
      :open="!!deleteGroup && !!deleteDependencies"
      title="删除前取消绑定"
      :description="deleteGroup ? `分组「${deleteGroup.name}」仍有关联资源，请先确认处理方式。` : ''"
      width="standard"
      :busy="deletingID !== null"
      @close="closeDeleteDialog"
    >
      <div v-if="deleteDependencies" class="group-delete-dialog">
        <dl class="group-delete-summary">
          <div><dt>API 密钥</dt><dd>{{ deleteDependencies.dependencies.api_keys }}<small>专属 {{ deleteDependencies.dependencies.exclusive_api_keys }} · 共享 {{ deleteDependencies.dependencies.shared_api_keys }}</small></dd></div>
          <div><dt>上游账号</dt><dd>{{ deleteDependencies.dependencies.accounts }}<small>专属 {{ deleteDependencies.dependencies.exclusive_accounts }} · 共享 {{ deleteDependencies.dependencies.shared_accounts }}</small></dd></div>
          <div v-if="deleteDependencies.dependencies.subscriptions"><dt>用户订阅</dt><dd>{{ deleteDependencies.dependencies.subscriptions }}</dd></div>
          <div v-if="deleteDependencies.dependencies.user_rates"><dt>专属倍率</dt><dd>{{ deleteDependencies.dependencies.user_rates }}</dd></div>
          <div v-if="deleteDependencies.dependencies.image_models"><dt>图像路由</dt><dd>{{ deleteDependencies.dependencies.image_models }}</dd></div>
          <div v-if="deleteDependencies.dependencies.alert_rules"><dt>告警规则</dt><dd>{{ deleteDependencies.dependencies.alert_rules }}</dd></div>
        </dl>

        <label class="modal-field">
          <span class="label">专属资源迁移到</span>
          <select v-model.number="deleteTargetGroupID" class="input">
            <option :value="0">不迁移，删除专属密钥和账号</option>
            <option v-for="target in deleteDependencies.target_groups" :key="target.id" :value="target.id">{{ target.name }} · {{ target.status === 'active' ? '启用' : '停用' }}</option>
          </select>
          <small class="modal-field__hint">共享密钥和账号只解除当前分组；专属资源、用户订阅、倍率、图像路由与告警会迁移到所选分组。</small>
        </label>

        <p v-if="deleteTargetGroupID === 0" class="group-delete-warning">
          不迁移将删除 {{ deleteDependencies.dependencies.exclusive_api_keys }} 个专属密钥和 {{ deleteDependencies.dependencies.exclusive_accounts }} 个专属账号。历史用量仍会保留。
        </p>
        <p v-else class="group-delete-target">
          专属资源将迁移到「{{ deleteDependencies.target_groups.find((item) => item.id === deleteTargetGroupID)?.name }}」，迁移完成后删除原分组。
        </p>
      </div>
      <template #footer>
        <button type="button" class="btn-ghost" :disabled="deletingID !== null" @click="closeDeleteDialog">返回</button>
        <button type="button" class="btn-danger" :disabled="deletingID !== null" @click="confirmUnbindAndDelete">{{ deletingID !== null ? '处理中…' : '取消绑定并删除' }}</button>
      </template>
    </AppModal>
  </div>
</template>
