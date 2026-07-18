<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { ModelCatalogueItem } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'

const models = ref<ModelCatalogueItem[]>([])
const loading = ref(true)
const error = ref('')
const platform = ref('all')
const kind = ref('all')
const query = ref('')

const filtered = computed(() => models.value.filter((model) => {
  const keyword = query.value.trim().toLowerCase()
  return (platform.value === 'all' || model.platform === platform.value)
    && (kind.value === 'all' || model.kind === kind.value)
    && (!keyword || `${model.name} ${model.description}`.toLowerCase().includes(keyword))
}))

const counts = computed(() => ({
  all: models.value.length,
  openai: models.value.filter((model) => model.platform === 'openai').length,
  anthropic: models.value.filter((model) => model.platform === 'anthropic').length,
  gemini: models.value.filter((model) => model.platform === 'gemini').length,
}))

function pricing(value: number | undefined) {
  if (!value) return '—'
  return `$${value.toFixed(value >= 1 ? 2 : 3)}`
}

function formatLimit(value: number, item: ModelCatalogueItem, field: 'context' | 'output') {
  if (!value) {
    if (item.kind === 'image') return field === 'context' ? '专用接口' : '按图像规格'
    return '未公开'
  }
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value % 1_000_000 ? 1 : 0)}M`
  if (value >= 1000) return `${(value / 1000).toFixed(value % 1000 ? 1 : 0)}K`
  return value.toLocaleString()
}

function capabilities(item: ModelCatalogueItem) {
  const values: string[] = []
  if (item.supports_vision) values.push('视觉')
  if (item.supports_tools) values.push('工具调用')
  if (item.supports_reasoning) values.push('推理')
  if (item.kind === 'image') values.push('生图 / 编辑')
  return values
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const received = await api.get<ModelCatalogueItem[] | null>('/api/user/model-catalog')
    models.value = Array.isArray(received) ? received.map((item) => ({
      ...item,
      groups: Array.isArray(item.groups) ? item.groups : [],
    })) : []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '模型目录暂时不可用'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="model-plaza-page">
    <header class="console-page-head model-plaza-head">
      <div>
        <div class="model-plaza-eyebrow"><span></span> 模型广场</div>
        <h1>选择适合你的模型</h1>
        <p>价格来自当前模型定价；实际扣费会使用本次请求所路由分组的倍率。</p>
      </div>
      <RouterLink class="btn-primary" to="/keys">创建 API 密钥</RouterLink>
    </header>

    <section class="model-plaza-toolbar" aria-label="模型筛选">
      <div class="model-platform-tabs">
        <button type="button" :class="{ 'is-active': platform === 'all' }" @click="platform = 'all'">全部 <b>{{ counts.all }}</b></button>
        <button type="button" :class="{ 'is-active': platform === 'openai' }" @click="platform = 'openai'">OpenAI <b>{{ counts.openai }}</b></button>
        <button type="button" :class="{ 'is-active': platform === 'anthropic' }" @click="platform = 'anthropic'">Claude <b>{{ counts.anthropic }}</b></button>
        <button type="button" :class="{ 'is-active': platform === 'gemini' }" @click="platform = 'gemini'">Gemini <b>{{ counts.gemini }}</b></button>
      </div>
      <select v-model="kind" class="input model-kind-select"><option value="all">全部类型</option><option value="chat">对话模型</option><option value="image">图像模型</option></select>
      <input v-model="query" class="input model-search" placeholder="搜索模型名或说明" />
    </section>

    <div v-if="loading" class="model-plaza-loading">正在加载模型目录…</div>
    <div v-else-if="error" class="ops-error-state"><span>{{ error }}</span><button class="btn-ghost !px-3 !py-1 text-xs" @click="load">重试</button></div>
    <div v-else-if="!filtered.length" class="model-plaza-empty"><strong>没有符合条件的模型</strong><span>换个筛选条件，或请管理员在模型配置里启用模型。</span></div>

    <section v-else class="model-card-grid">
      <article v-for="item in filtered" :key="item.id" class="model-card" :class="{ 'is-unavailable': !item.available }">
        <div class="model-card-scroll">
          <div class="model-card-top">
            <div class="model-card-title"><span class="model-platform-label">{{ PLATFORM_LABELS[item.platform] || item.platform }}</span><h2>{{ item.name }}</h2></div>
            <span :class="item.available ? 'tag-green' : 'tag-amber'">{{ item.available ? '可调用' : '暂无可用上游' }}</span>
          </div>
          <p class="model-description">{{ item.description || '尚未添加模型说明。' }}</p>
          <div class="model-capabilities"><span v-for="capability in capabilities(item)" :key="capability">{{ capability }}</span><span v-if="!capabilities(item).length">通用对话</span></div>
          <dl class="model-limits"><div><dt>上下文</dt><dd>{{ formatLimit(item.context_window, item, 'context') }}</dd></div><div><dt>最大输出</dt><dd>{{ formatLimit(item.max_output_tokens, item, 'output') }}</dd></div><div><dt>接口</dt><dd>{{ item.kind === 'image' ? 'Images' : 'Chat' }}</dd></div></dl>
          <div class="model-price-box">
            <template v-if="item.kind === 'image' && item.pricing?.image_price_per_image"><span>参考单价</span><strong>{{ pricing(item.pricing.image_price_per_image) }}<em>/ 张</em></strong></template>
            <template v-else><span>每百万 Token · 输入 / 输出</span><strong>{{ pricing(item.pricing?.input_price) }} <em>/</em> {{ pricing(item.pricing?.output_price) }}</strong></template>
          </div>
          <div class="model-groups"><span class="model-groups-label">可选分组</span><div><span v-for="group in item.groups" :key="group.id" :class="group.ready ? 'is-ready' : ''">{{ group.name }} ×{{ item.kind === 'image' && group.image_rate_independent ? group.image_rate_multiplier : group.rate_multiplier }}</span><em v-if="!item.groups.length">暂无可用分组</em></div></div>
        </div>
        <RouterLink to="/keys" class="model-card-action">用这个模型 <span>→</span></RouterLink>
      </article>
    </section>
  </div>
</template>
