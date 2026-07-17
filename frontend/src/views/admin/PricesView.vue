<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, withToast } from '../../api/client'
import type { ModelPrice } from '../../api/types'
import { PLATFORM_LABELS } from '../../api/types'

const prices = ref<ModelPrice[]>([])
const showForm = ref(false)
const editing = ref<ModelPrice | null>(null)

const form = ref({
  match: '',
  platform: 'anthropic',
  input_price: 0,
  output_price: 0,
  cache_read_price: 0,
  cache_write_price: 0,
	cache_write_5m_price: 0,
	cache_write_1h_price: 0,
	image_input_price: 0,
	image_output_price: 0,
	image_cache_read_price: 0,
	image_price_per_image: 0,
})

async function load() {
  prices.value = await api.get<ModelPrice[]>('/api/admin/prices')
}
onMounted(load)

function openCreate() {
  editing.value = null
  form.value = {
    match: '', platform: 'anthropic', input_price: 0, output_price: 0,
    cache_read_price: 0, cache_write_price: 0, cache_write_5m_price: 0, cache_write_1h_price: 0,
    image_input_price: 0, image_output_price: 0, image_cache_read_price: 0,
		image_price_per_image: 0,
  }
  showForm.value = true
}

function openEdit(p: ModelPrice) {
  editing.value = p
  form.value = {
    match: p.match,
    platform: p.platform,
    input_price: p.input_price,
    output_price: p.output_price,
    cache_read_price: p.cache_read_price,
    cache_write_price: p.cache_write_price,
		cache_write_5m_price: p.cache_write_5m_price || 0,
		cache_write_1h_price: p.cache_write_1h_price || 0,
		image_input_price: p.image_input_price || 0,
		image_output_price: p.image_output_price || 0,
		image_cache_read_price: p.image_cache_read_price || 0,
		image_price_per_image: p.image_price_per_image || 0,
  }
  showForm.value = true
}

async function save() {
  const body = {
    ...form.value,
    input_price: Number(form.value.input_price),
    output_price: Number(form.value.output_price),
    cache_read_price: Number(form.value.cache_read_price),
    cache_write_price: Number(form.value.cache_write_price),
		cache_write_5m_price: Number(form.value.cache_write_5m_price),
		cache_write_1h_price: Number(form.value.cache_write_1h_price),
		image_input_price: Number(form.value.image_input_price),
		image_output_price: Number(form.value.image_output_price),
		image_cache_read_price: Number(form.value.image_cache_read_price),
		image_price_per_image: Number(form.value.image_price_per_image),
  }
  const ok = await withToast(() => api.post('/api/admin/prices', body), '已保存')
  if (ok !== null) {
    showForm.value = false
    await load()
  }
}

async function remove(p: ModelPrice) {
  if (!confirm(`确认删除定价规则「${p.match}」?`)) return
  await withToast(() => api.delete(`/api/admin/prices/${p.id}`), '已删除')
  await load()
}
</script>

<template>
  <div>
    <div class="console-page-head">
      <div>
        <h1>模型定价</h1>
				<p class="mt-1 text-sm text-slate-500">文本与图像 Token 单位为 USD / 1M；每张图为 USD / 张。设定每张图价格后，会覆盖图像 Token 三项，避免重复计费。</p>
      </div>
      <button class="btn-primary" @click="openCreate">新增规则</button>
    </div>

    <div class="card overflow-x-auto">
      <table class="table-base">
        <thead>
          <tr>
            <th>匹配规则</th>
            <th>平台</th>
            <th class="text-right">输入</th>
            <th class="text-right">输出</th>
				<th class="text-right">缓存命中</th>
				<th class="text-right">短缓存 5m</th>
				<th class="text-right">长缓存 1h</th>
				<th class="text-right">默认写入</th>
			<th class="text-right">图像输入</th>
			<th class="text-right">图像输出</th>
			<th class="text-right">图像缓存读</th>
			<th class="text-right">每张图</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in prices" :key="p.id">
            <td class="font-mono text-sm text-slate-200">{{ p.match }}</td>
            <td><span class="tag-gray">{{ PLATFORM_LABELS[p.platform] || p.platform || '-' }}</span></td>
            <td class="num text-right">${{ p.input_price }}</td>
            <td class="num text-right">${{ p.output_price }}</td>
            <td class="num text-right text-slate-500">${{ p.cache_read_price }}</td>
				<td class="num text-right text-slate-500">{{ p.cache_write_5m_price ? `$${p.cache_write_5m_price}` : '—' }}</td>
				<td class="num text-right text-slate-500">{{ p.cache_write_1h_price ? `$${p.cache_write_1h_price}` : '—' }}</td>
            <td class="num text-right text-slate-500">${{ p.cache_write_price }}</td>
			<td class="num text-right text-slate-500">${{ p.image_input_price || '-' }}</td>
			<td class="num text-right text-slate-500">${{ p.image_output_price || '-' }}</td>
			<td class="num text-right text-slate-500">${{ p.image_cache_read_price || '-' }}</td>
			<td class="num text-right text-amber">{{ p.image_price_per_image ? `$${p.image_price_per_image}` : '—' }}</td>
            <td class="text-right">
              <button class="btn-ghost !px-2.5 !py-1 text-xs" @click="openEdit(p)">编辑</button>
              <button class="btn-danger ml-2 !px-2.5 !py-1 text-xs" @click="remove(p)">删除</button>
            </td>
          </tr>
			<tr v-if="prices.length === 0"><td colspan="13" class="py-10 text-center text-slate-500">尚未设置定价规则</td></tr>
        </tbody>
      </table>
    </div>

    <Teleport to="body">
      <div v-if="showForm" class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm" @click.self="showForm = false">
        <div class="card w-full max-w-2xl p-6">
          <h3 class="mb-5 text-base font-semibold text-slate-100">{{ editing ? '编辑定价' : '新增定价' }}</h3>
          <div class="space-y-4">
            <div>
              <label class="label">匹配规则</label>
              <input v-model="form.match" class="input font-mono" placeholder="claude-sonnet-* 或精确模型名" :disabled="!!editing" />
            </div>
            <div>
              <label class="label">平台(标注用)</label>
              <select v-model="form.platform" class="input">
                <option value="anthropic">Claude</option>
                <option value="openai">OpenAI</option>
                <option value="gemini">Gemini</option>
              </select>
            </div>
            <div class="grid grid-cols-2 gap-4">
              <div>
                <label class="label">输入 $/1M</label>
                <input v-model.number="form.input_price" type="number" step="0.01" min="0" class="input" />
              </div>
              <div>
                <label class="label">输出 $/1M</label>
                <input v-model.number="form.output_price" type="number" step="0.01" min="0" class="input" />
              </div>
              <div>
                <label class="label">缓存命中 $/1M</label>
                <input v-model.number="form.cache_read_price" type="number" step="0.01" min="0" class="input" />
              </div>
              <div>
                <label class="label">默认缓存写 $/1M</label>
                <input v-model.number="form.cache_write_price" type="number" step="0.01" min="0" class="input" />
              </div>
					<div>
						<label class="label">短缓存 5m $/1M</label>
						<input v-model.number="form.cache_write_5m_price" type="number" step="0.01" min="0" class="input" placeholder="留空则用默认缓存写" />
					</div>
					<div>
						<label class="label">长缓存 1h $/1M</label>
						<input v-model.number="form.cache_write_1h_price" type="number" step="0.01" min="0" class="input" placeholder="留空则用默认缓存写" />
					</div>
				<div>
					<label class="label">图像输入 $/1M</label>
					<input v-model.number="form.image_input_price" type="number" step="0.01" min="0" class="input" />
				</div>
				<div>
					<label class="label">图像输出 $/1M</label>
					<input v-model.number="form.image_output_price" type="number" step="0.01" min="0" class="input" />
				</div>
				<div>
					<label class="label">图像缓存读 $/1M</label>
					<input v-model.number="form.image_cache_read_price" type="number" step="0.01" min="0" class="input" />
				</div>
				<div class="col-span-2 rounded-lg border border-amber/25 bg-amber/5 p-3">
					<label class="label !mb-1 text-amber">每张图固定价格（USD）</label>
					<p class="mb-2 text-xs leading-5 text-slate-500">适用于图片生成和编辑。大于 0 时按实际返回图片数收费，替代上面的图像 Token 单价。</p>
					<input v-model.number="form.image_price_per_image" type="number" step="0.001" min="0" class="input" placeholder="例如 0.08" />
				</div>
            </div>
            <div class="flex justify-end gap-3 pt-2">
              <button class="btn-ghost" @click="showForm = false">取消</button>
              <button class="btn-primary" :disabled="!form.match" @click="save">保存</button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
