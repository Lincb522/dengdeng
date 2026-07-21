<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { Group, ModelConfig } from '../../api/types'
import { PLATFORM_LABELS } from '../../api/types'

const models = ref<ModelConfig[]>([])
const groups = ref<Group[]>([])
const showForm = ref(false)
const editing = ref<ModelConfig | null>(null)
const form = ref({ name: '', platform: 'openai', kind: 'chat', upstream_model: '', context_window: 0, max_output_tokens: 0, supports_vision: false, supports_tools: false, supports_reasoning: false, image_group_id: 0, description: '', status: 'active' })

const matchingImageGroups = computed(() => groups.value.filter((group) => group.platform === form.value.platform))
function imageGroupName(groupID: number) { return groups.value.find((group) => group.id === groupID)?.name || `分组 #${groupID}` }
function limitLabel(item: ModelConfig, value: number, field: 'context' | 'output') {
  if (value) return value.toLocaleString()
  if (item.kind === 'image') return field === 'context' ? '专用接口' : '按图像规格'
  return '未公开'
}

async function load() {
  const [modelRows, groupRows] = await Promise.all([
    api.get<ModelConfig[]>('/api/admin/models'),
    api.get<Group[]>('/api/admin/groups'),
  ])
  models.value = modelRows
  groups.value = groupRows
}
onMounted(load)
function create() { editing.value = null; form.value = { name: '', platform: 'openai', kind: 'chat', upstream_model: '', context_window: 0, max_output_tokens: 0, supports_vision: false, supports_tools: false, supports_reasoning: false, image_group_id: 0, description: '', status: 'active' }; showForm.value = true }
function edit(item: ModelConfig) { editing.value = item; form.value = { name: item.name, platform: item.platform, kind: item.kind, upstream_model: item.upstream_model, context_window: item.context_window || 0, max_output_tokens: item.max_output_tokens || 0, supports_vision: !!item.supports_vision, supports_tools: !!item.supports_tools, supports_reasoning: !!item.supports_reasoning, image_group_id: item.image_group_id || 0, description: item.description, status: item.status }; showForm.value = true }
async function save() { const ok = await withToast(() => api.post('/api/admin/models', form.value), '模型配置已保存'); if (ok !== null) { showForm.value = false; await load() } }
async function remove(item: ModelConfig) { if (!confirm(`删除模型配置「${item.name}」?`)) return; await withToast(() => api.delete(`/api/admin/models/${item.id}`), '已删除'); await load() }
</script>

<template>
  <div>
    <div class="mb-6 flex items-center justify-between"><div><h1 class="text-xl font-bold text-slate-100">模型配置</h1><p class="mt-1 text-sm text-slate-500">对外模型名可映射为上游模型名；生图模型还可独立指定账号分组。</p></div><button class="btn-primary" @click="create">新增模型</button></div>
    <div class="card overflow-x-auto"><table v-responsive-table class="table-base"><thead><tr><th>对外模型名</th><th>平台</th><th>类型</th><th>上下文 / 输出</th><th>上游模型名</th><th>生图上游</th><th>说明</th><th>状态</th><th class="text-right">操作</th></tr></thead><tbody>
      <tr v-for="item in models" :key="item.id"><td class="font-mono text-sm text-slate-100">{{ item.name }}</td><td><span class="tag-gray">{{ PLATFORM_LABELS[item.platform] || item.platform }}</span></td><td><span :class="item.kind === 'image' ? 'tag-cyan' : 'tag-gray'">{{ item.kind === 'image' ? '生图 / 编辑' : '对话' }}</span></td><td class="num text-xs text-slate-400">{{ limitLabel(item, item.context_window, 'context') }} / {{ limitLabel(item, item.max_output_tokens, 'output') }}</td><td class="font-mono text-xs text-slate-400">{{ item.upstream_model || '保持原名' }}</td><td class="text-xs text-slate-400">{{ item.kind === 'image' ? (item.image_group_id ? imageGroupName(item.image_group_id) : '跟随密钥分组') : '—' }}</td><td class="max-w-xs truncate text-slate-500" :title="item.description">{{ item.description || '-' }}</td><td><span :class="item.status === 'active' ? 'tag-green' : 'tag-red'">{{ item.status === 'active' ? '启用' : '禁用' }}</span></td><td class="whitespace-nowrap text-right"><button class="btn-ghost !px-2.5 !py-1 text-xs" @click="edit(item)">编辑</button><button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="remove(item)">删除</button></td></tr>
      <tr v-if="models.length === 0"><td colspan="9" class="py-10 text-center text-slate-500">暂无模型配置。可添加对外别名或禁用规则。</td></tr>
    </tbody></table></div>
    <Teleport to="body"><div v-if="showForm" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="showForm = false"><div class="card w-full max-w-lg p-6"><h3 class="mb-5 text-base font-semibold text-slate-100">{{ editing ? '编辑模型配置' : '新增模型配置' }}</h3><div class="space-y-4">
      <div><label class="label">对外模型名</label><input v-model.trim="form.name" class="input font-mono" placeholder="gpt-5.6 或 my-image" :disabled="!!editing" /></div>
      <div class="grid grid-cols-2 gap-4"><div><label class="label">平台</label><select v-model="form.platform" class="input" @change="form.image_group_id = 0"><option value="openai">OpenAI</option><option value="anthropic">Claude</option><option value="gemini">Gemini</option><option value="grok">Grok</option></select></div><div><label class="label">类型</label><select v-model="form.kind" class="input" @change="form.kind !== 'image' && (form.image_group_id = 0)"><option value="chat">对话</option><option value="image">生图 / 编辑</option></select></div></div>
      <div><label class="label">实际上游模型名（可选）</label><input v-model.trim="form.upstream_model" class="input font-mono" placeholder="留空则原样转发" /></div><div class="grid grid-cols-2 gap-4"><label><span class="label">上下文窗口</span><input v-model.number="form.context_window" type="number" min="0" step="1" class="input num" placeholder="0 = 未公开或不适用" /></label><label><span class="label">最大输出 Token</span><input v-model.number="form.max_output_tokens" type="number" min="0" step="1" class="input num" placeholder="0 = 未公开或不适用" /></label></div><div class="rounded-xl border border-slate-800 bg-slate-950/35 p-3"><span class="label">能力标签</span><div class="mt-2 flex flex-wrap gap-4 text-sm text-slate-300"><label class="inline-flex items-center gap-2"><input v-model="form.supports_vision" type="checkbox" />视觉</label><label class="inline-flex items-center gap-2"><input v-model="form.supports_tools" type="checkbox" />工具调用</label><label class="inline-flex items-center gap-2"><input v-model="form.supports_reasoning" type="checkbox" />推理</label></div></div><div><label class="label">说明（可选）</label><input v-model.trim="form.description" class="input" placeholder="模型用途或版本说明" /></div><div><label class="label">状态</label><select v-model="form.status" class="input"><option value="active">启用</option><option value="disabled">禁用</option></select></div>
      <div v-if="form.kind === 'image'" class="rounded-xl border border-slate-800 bg-slate-950/35 p-4"><label class="label !mb-1">生图专用上游分组</label><p class="mb-3 text-xs leading-5 text-slate-500">图片生成和编辑请求会从此分组的账号池、BASE URL 和代理配置转发；不选则跟随调用密钥的分组。</p><select v-model.number="form.image_group_id" class="input"><option :value="0">跟随密钥分组</option><option v-for="group in matchingImageGroups" :key="group.id" :value="group.id">{{ group.name }}</option></select></div>
      <div class="flex justify-end gap-3 pt-2"><button class="btn-ghost" @click="showForm = false">取消</button><button class="btn-primary" :disabled="!form.name" @click="save">保存</button></div>
    </div></div></div></Teleport>
  </div>
</template>
