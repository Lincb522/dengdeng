<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { Group } from '../../api/types'
import { PLATFORM_LABELS } from '../../api/types'

const groups = ref<Group[]>([])
const showForm = ref(false)
const editing = ref<Group | null>(null)

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
    is_public: true, status: 'active',
  }
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
    is_public: g.is_public,
    status: g.status,
  }
  showForm.value = true
}

async function save() {
  const body = {
    ...form.value,
    rate_multiplier: Number(form.value.rate_multiplier),
    cache_read_multiplier: Number(form.value.cache_read_multiplier),
    cache_write_5m_multiplier: Number(form.value.cache_write_5m_multiplier),
    cache_write_1h_multiplier: Number(form.value.cache_write_1h_multiplier),
    image_rate_multiplier: Number(form.value.image_rate_multiplier),
  }
  const ok = editing.value
    ? await withToast(() => api.put(`/api/admin/groups/${editing.value!.id}`, body), '已保存')
    : await withToast(() => api.post('/api/admin/groups', body), '分组已创建')
  if (ok !== null) {
    showForm.value = false
    await load()
  }
}

async function remove(g: Group) {
  if (!confirm(`确认删除分组「${g.name}」?其下上游账号将一并删除。`)) return
  await withToast(() => api.delete(`/api/admin/groups/${g.id}`), '已删除')
  await load()
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
        <p class="mt-1 text-sm text-slate-500">分组 = 平台 + 账号池 + 计费倍率</p>
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
              <button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="remove(g)">删除</button>
            </td>
          </tr>
          <tr v-if="!groups.length">
            <td colspan="7" class="py-10 text-center text-sm text-slate-500">暂无分组</td>
          </tr>
        </tbody>
      </table>
    </div>

    <Teleport to="body">
      <div v-if="showForm" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="showForm = false">
        <div class="card w-full max-w-2xl p-6">
          <h3 class="mb-5 text-base font-semibold text-slate-100">{{ editing ? '编辑分组' : '新建分组' }}</h3>
          <div class="space-y-4">
            <div>
              <label class="label">名称</label>
              <input v-model="form.name" class="input" placeholder="例如:claude-standard" />
            </div>
            <div>
              <label class="label">平台</label>
              <select v-model="form.platform" class="input" :disabled="!!editing">
                <option value="anthropic">Claude (Anthropic)</option>
                <option value="openai">OpenAI</option>
                <option value="gemini">Gemini</option>
                <option value="grok">Grok (xAI)</option>
              </select>
            </div>
            <div>
              <label class="label">描述</label>
              <input v-model="form.description" class="input" placeholder="可选" />
            </div>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label">基础倍率</label>
                <input v-model.number="form.rate_multiplier" type="number" step="0.1" min="0.1" class="input" />
              </div>
              <div>
                <label class="label">状态</label>
                <select v-model="form.status" class="input">
                  <option value="active">启用</option>
                  <option value="disabled">停用</option>
                </select>
              </div>
            </div>
				<div class="rounded-xl border border-slate-800 bg-slate-950/35 p-4">
					<div class="mb-1 flex items-center justify-between">
						<label class="label !mb-0">缓存倍率</label>
						<span class="text-[11px] text-slate-500">在基础倍率之上叠加</span>
					</div>
					<p class="mb-3 text-xs leading-5 text-slate-500">缓存命中、短缓存创建和长缓存创建分开计费；没有 TTL 明细的旧上游响应按 5m 规则处理。</p>
					<div class="grid grid-cols-3 gap-3">
						<div>
							<label class="label">命中</label>
							<input v-model.number="form.cache_read_multiplier" type="number" step="0.1" min="0.1" class="input" />
						</div>
						<div>
							<label class="label">短缓存 5m</label>
							<input v-model.number="form.cache_write_5m_multiplier" type="number" step="0.1" min="0.1" class="input" />
						</div>
						<div>
							<label class="label">长缓存 1h</label>
							<input v-model.number="form.cache_write_1h_multiplier" type="number" step="0.1" min="0.1" class="input" />
						</div>
					</div>
				</div>
				<div class="rounded-xl border border-slate-800 bg-slate-950/35 p-4">
					<div class="flex items-center justify-between gap-4">
						<div>
							<label class="label !mb-1">图像独立倍率</label>
							<p class="text-xs text-slate-500">开启后，单独计费的图像 token 不再继承基础倍率。</p>
						</div>
						<label class="flex shrink-0 items-center gap-2 text-sm text-slate-300">
							<input v-model="form.image_rate_independent" type="checkbox" class="h-4 w-4 accent-amber" />
							独立
						</label>
					</div>
					<div class="mt-3 max-w-[180px]">
						<label class="label">图像倍率</label>
						<input v-model.number="form.image_rate_multiplier" :disabled="!form.image_rate_independent" type="number" step="0.1" min="0.1" class="input disabled:cursor-not-allowed disabled:opacity-50" />
					</div>
				</div>
            <label class="flex items-center gap-2 text-sm text-slate-300">
              <input v-model="form.is_public" type="checkbox" class="h-4 w-4 accent-amber" />
              对普通用户开放(可自助创建密钥)
            </label>
            <div class="flex justify-end gap-3 pt-2">
              <button class="btn-ghost" @click="showForm = false">取消</button>
              <button class="btn-primary" :disabled="!form.name" @click="save">保存</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
