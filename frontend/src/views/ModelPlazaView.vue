<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, getToken } from '../api/client'
import type { ModelCatalogueItem } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'
import BrandMark from '../components/BrandMark.vue'
import ProviderLogo from '../components/ProviderLogo.vue'
import ThemeToggle from '../components/ThemeToggle.vue'

const models = ref<ModelCatalogueItem[]>([])
const loading = ref(true)
const error = ref('')
const platform = ref('all')
const kind = ref('all')
const query = ref('')
const isSignedIn = Boolean(getToken())
const accountTarget = isSignedIn ? '/dashboard' : '/login'
const accountLabel = isSignedIn ? '控制台' : '登录'
const modelActionTarget = isSignedIn ? '/keys' : '/login'
const modelActionLabel = isSignedIn ? '创建密钥' : '登录后使用'
const providerOrder = ['openai', 'anthropic', 'gemini', 'grok', 'kimi', 'zhipu', 'deepseek']

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
  grok: models.value.filter((model) => model.platform === 'grok').length,
  kimi: models.value.filter((model) => model.platform === 'kimi').length,
  zhipu: models.value.filter((model) => model.platform === 'zhipu').length,
  deepseek: models.value.filter((model) => model.platform === 'deepseek').length,
}))
const availableCount = computed(() => models.value.filter((model) => model.available).length)
const providerFilters = computed(() => {
  const grouped = new Map<string, number>()
  for (const model of models.value) grouped.set(model.platform, (grouped.get(model.platform) || 0) + 1)
  return [...grouped.entries()]
    .sort(([left], [right]) => {
      const leftIndex = providerOrder.indexOf(left)
      const rightIndex = providerOrder.indexOf(right)
      return (leftIndex < 0 ? 99 : leftIndex) - (rightIndex < 0 ? 99 : rightIndex) || left.localeCompare(right)
    })
    .map(([id, count]) => ({ id, count, label: PLATFORM_LABELS[id] || id }))
})

function pricing(value: number | undefined) {
  if (!value) return '暂无'
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

function providerClass(platformName: string) {
  return `is-${platformName.trim().toLowerCase().replace(/[^a-z0-9-]/g, '-') || 'other'}`
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const received = await api.get<ModelCatalogueItem[] | null>('/api/models')
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
  <main class="public-models-shell">
    <header class="public-models-topbar">
      <RouterLink to="/" class="public-models-brand" aria-label="返回 DengDeng AI 主页">
        <BrandMark :size="34" />
        <span><strong>DengDeng AI</strong><small>模型广场</small></span>
      </RouterLink>
      <div class="public-models-actions">
        <RouterLink to="/" class="public-models-home">首页</RouterLink>
        <RouterLink :to="accountTarget" class="public-models-login">{{ accountLabel }}</RouterLink>
        <ThemeToggle />
      </div>
    </header>

    <div class="public-models-main">
      <header class="public-models-heading">
        <div>
          <h1>模型广场</h1>
          <p>上下文、输出限制、计费和可选分组</p>
        </div>
        <dl class="public-models-summary">
          <div><dt>全部</dt><dd>{{ counts.all }}</dd></div>
          <div><dt>可调用</dt><dd>{{ availableCount }}</dd></div>
          <div><dt>服务商</dt><dd>{{ providerFilters.length }}</dd></div>
        </dl>
      </header>

      <section class="catalog-toolbar" aria-label="模型筛选">
        <div class="catalog-provider-tabs">
          <button type="button" :class="{ 'is-active': platform === 'all' }" @click="platform = 'all'">全部 <b>{{ counts.all }}</b></button>
          <button v-for="provider in providerFilters" :key="provider.id" type="button" :class="{ 'is-active': platform === provider.id }" @click="platform = provider.id"><ProviderLogo :platform="provider.id" size="sm" />{{ provider.label }} <b>{{ provider.count }}</b></button>
        </div>
        <div class="catalog-fields">
          <select v-model="kind" class="input catalog-kind-select"><option value="all">全部类型</option><option value="chat">对话模型</option><option value="image">图像模型</option></select>
          <label class="catalog-search"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7"></circle><path d="m20 20-4-4"></path></svg><input v-model="query" placeholder="搜索模型" aria-label="搜索模型" /></label>
        </div>
      </section>

      <div v-if="loading" class="catalog-loading" aria-label="正在加载模型目录">
        <span v-for="index in 6" :key="index"></span>
      </div>
      <div v-else-if="error" class="ops-error-state"><span>{{ error }}</span><button class="btn-ghost !px-3 !py-1 text-xs" @click="load">重试</button></div>
      <div v-else-if="!filtered.length" class="catalog-empty"><strong>没有符合条件的模型</strong><span>请调整筛选条件</span></div>

      <div v-else>
        <div class="catalog-result"><strong>{{ filtered.length }} 个模型</strong><span>{{ filtered.filter((item) => item.available).length }} 个可调用</span></div>
        <section class="catalog-grid">
          <article v-for="item in filtered" :key="item.id" class="catalog-card" :class="[providerClass(item.platform), { 'is-unavailable': !item.available }]">
            <div class="catalog-card-cover">
              <div class="catalog-card-provider"><ProviderLogo :platform="item.platform" size="lg" /><span>{{ PLATFORM_LABELS[item.platform] || item.platform }}</span></div>
              <span class="catalog-card-status" :class="item.available ? 'is-ready' : 'is-offline'"><i></i>{{ item.available ? '可调用' : '暂无上游' }}</span>
              <h2 :title="item.name">{{ item.name }}</h2>
              <div class="catalog-card-kind"><span>{{ item.kind === 'image' ? '图像模型' : '对话模型' }}</span><span>{{ formatLimit(item.context_window, item, 'context') }} 上下文</span></div>
            </div>
            <div class="catalog-card-body">
              <p class="catalog-description">{{ item.description || '暂无说明' }}</p>
              <div class="catalog-capabilities"><span v-for="capability in capabilities(item)" :key="capability">{{ capability }}</span><span v-if="!capabilities(item).length">通用对话</span></div>
              <dl class="catalog-specs"><div><dt>最大输出</dt><dd>{{ formatLimit(item.max_output_tokens, item, 'output') }}</dd></div><div><dt>接口</dt><dd>{{ item.kind === 'image' ? 'Images' : 'Chat' }}</dd></div></dl>
              <div class="catalog-price">
                <template v-if="item.kind === 'image' && item.pricing?.image_price_per_image"><span>参考单价</span><strong>{{ pricing(item.pricing.image_price_per_image) }}<em>/ 张</em></strong></template>
                <template v-else><span>每百万 Token · 输入 / 输出</span><strong>{{ pricing(item.pricing?.input_price) }} <em>/</em> {{ pricing(item.pricing?.output_price) }}</strong></template>
              </div>
              <div class="catalog-groups"><div><span>可选分组</span><b>{{ item.groups.length }}</b></div><div class="catalog-group-tags"><span v-for="group in item.groups" :key="group.id" :class="{ 'is-ready': group.ready }">{{ group.name }} ×{{ item.kind === 'image' && group.image_rate_independent ? group.image_rate_multiplier : group.rate_multiplier }}</span><em v-if="!item.groups.length">暂无可用分组</em></div></div>
            </div>
            <RouterLink :to="modelActionTarget" class="catalog-card-action">{{ modelActionLabel }} <span>→</span></RouterLink>
          </article>
        </section>
      </div>
    </div>
  </main>
</template>

<style scoped>
.public-models-heading { display: flex; align-items: flex-end; justify-content: space-between; gap: 1rem; }
.public-models-summary { display: grid; grid-template-columns: repeat(3, auto); overflow: hidden; border: 1px solid var(--line); border-radius: .72rem; background: var(--surface); }
.public-models-summary > div { min-width: 5.2rem; padding: .62rem .8rem; border-right: 1px solid var(--line); }
.public-models-summary > div:last-child { border-right: 0; }
.public-models-summary dt { color: var(--ink-soft); font-size: .6rem; font-weight: 720; }
.public-models-summary dd { margin-top: .18rem; color: var(--ink); font-size: .84rem; font-weight: 850; }
.catalog-toolbar { display: grid; gap: .6rem; margin-bottom: 1rem; padding: .6rem; border: 1px solid var(--line); border-radius: .82rem; background: var(--surface); }
.catalog-provider-tabs { display: flex; min-width: 0; flex-wrap: wrap; gap: .3rem; }
.catalog-provider-tabs button { display: inline-flex; min-height: 2.1rem; align-items: center; gap: .32rem; padding: 0 .58rem; border: 1px solid transparent; border-radius: 999px; background: var(--surface-muted); color: var(--ink-soft); font-size: .65rem; font-weight: 740; white-space: nowrap; }
.catalog-provider-tabs button:hover { color: var(--ink); }
.catalog-provider-tabs button.is-active { border-color: color-mix(in srgb, var(--accent) 44%, var(--line)); background: color-mix(in srgb, var(--accent) 10%, var(--surface)); color: var(--ink); }
.catalog-provider-tabs b { color: var(--ink-faint); font-size: .58rem; }
.catalog-fields { display: grid; grid-template-columns: 8.5rem minmax(12rem, 22rem); justify-content: end; gap: .45rem; }
.catalog-kind-select { min-height: 2.35rem; padding-top: .35rem; padding-bottom: .35rem; font-size: .69rem; }
.catalog-search { display: flex; min-height: 2.35rem; align-items: center; gap: .45rem; padding: 0 .66rem; border: 1px solid var(--line); border-radius: .5rem; background: var(--surface); }
.catalog-search svg { width: .88rem; height: .88rem; flex: 0 0 auto; fill: none; stroke: var(--ink-soft); stroke-width: 1.8; }
.catalog-search input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--ink); font-size: .69rem; }
.catalog-result { display: flex; align-items: center; justify-content: space-between; gap: .75rem; margin: 0 0 .65rem; color: var(--ink-soft); font-size: .66rem; }
.catalog-result strong { color: var(--ink); font-size: .72rem; }
.catalog-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr)); gap: .85rem; }
.catalog-card { --provider-bg: #e6ece9; --provider-ink: #263d35; display: flex; min-width: 0; overflow: hidden; border: 1px solid var(--line); border-radius: .86rem; background: var(--surface); flex-direction: column; transition: border-color .18s ease, transform .18s ease; }
.catalog-card:hover { border-color: color-mix(in srgb, var(--provider-ink) 40%, var(--line)); transform: translateY(-2px); }
.catalog-card.is-anthropic { --provider-bg: #f3d8cf; --provider-ink: #7a3825; }
.catalog-card.is-gemini { --provider-bg: #dedbfa; --provider-ink: #463c91; }
.catalog-card.is-grok { --provider-bg: #e2e3e5; --provider-ink: #27292d; }
.catalog-card.is-kimi { --provider-bg: #d9e8f6; --provider-ink: #273f57; }
.catalog-card.is-zhipu { --provider-bg: #dce3ff; --provider-ink: #304ba9; }
.catalog-card.is-deepseek { --provider-bg: #d7e6ff; --provider-ink: #2f5ca9; }
.catalog-card.is-unavailable { opacity: .74; }
.catalog-card-cover { position: relative; display: grid; min-height: 9.4rem; align-content: space-between; overflow: hidden; gap: .7rem; padding: .85rem; background: var(--provider-bg); color: var(--provider-ink); }
.catalog-card-cover::after { position: absolute; top: -3.4rem; right: -2.4rem; width: 8.5rem; height: 8.5rem; border: 1.35rem solid color-mix(in srgb, var(--provider-ink) 7%, transparent); border-radius: 50%; content: ''; }
.catalog-card-provider { position: relative; z-index: 1; display: flex; align-items: center; gap: .45rem; color: var(--provider-ink); font-size: .63rem; font-weight: 800; }
.catalog-card-provider :deep(.provider-logo) { background: color-mix(in srgb, var(--surface) 74%, transparent); }
.catalog-card-status { position: absolute; z-index: 2; top: .85rem; right: .85rem; display: inline-flex; align-items: center; gap: .28rem; padding: .22rem .42rem; border-radius: 999px; background: color-mix(in srgb, var(--surface) 70%, transparent); font-size: .56rem; font-weight: 780; }
.catalog-card-status i { width: .38rem; height: .38rem; border-radius: 50%; background: currentColor; }
.catalog-card-status.is-ready { color: rgb(var(--dd-signal-green)); }
.catalog-card-status.is-offline { color: rgb(var(--dd-signal-red)); }
.catalog-card-cover h2 { position: relative; z-index: 1; max-width: 100%; color: var(--provider-ink); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 1.05rem; font-weight: 850; line-height: 1.25; overflow-wrap: anywhere; }
.catalog-card-kind { position: relative; z-index: 1; display: flex; align-items: center; justify-content: space-between; gap: .5rem; font-size: .58rem; font-weight: 720; }
.catalog-card-kind span { padding: .17rem .38rem; border-radius: .3rem; background: color-mix(in srgb, var(--surface) 56%, transparent); white-space: nowrap; }
.catalog-card-body { display: flex; min-height: 15rem; flex: 1; flex-direction: column; padding: .85rem; }
.catalog-description { display: -webkit-box; min-height: 2.35rem; overflow: hidden; color: var(--ink-soft); font-size: .68rem; line-height: 1.55; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.catalog-capabilities { display: flex; min-width: 0; flex-wrap: wrap; gap: .3rem; margin-top: .65rem; }
.catalog-capabilities span { padding: .16rem .38rem; border-radius: .32rem; background: var(--surface-muted); color: var(--ink-soft); font-size: .56rem; font-weight: 680; white-space: nowrap; }
.catalog-specs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); overflow: hidden; margin-top: .72rem; border: 1px solid var(--line); border-radius: .52rem; }
.catalog-specs > div { min-width: 0; padding: .55rem .6rem; }
.catalog-specs > div:first-child { border-right: 1px solid var(--line); }
.catalog-specs dt { color: var(--ink-soft); font-size: .56rem; }
.catalog-specs dd { overflow: hidden; margin-top: .2rem; color: var(--ink); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .68rem; font-weight: 800; text-overflow: ellipsis; white-space: nowrap; }
.catalog-price { margin-top: .72rem; }
.catalog-price > span { color: var(--ink-soft); font-size: .56rem; }
.catalog-price strong { display: block; margin-top: .2rem; color: var(--ink); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .83rem; }
.catalog-price em { color: var(--ink-soft); font-family: inherit; font-size: .62rem; font-style: normal; font-weight: 620; }
.catalog-groups { min-width: 0; margin-top: auto; padding-top: .75rem; }
.catalog-groups > div:first-child { display: flex; align-items: center; justify-content: space-between; color: var(--ink-soft); font-size: .57rem; }
.catalog-groups b { display: grid; min-width: 1.25rem; height: 1.25rem; place-items: center; border-radius: .34rem; background: var(--surface-muted); color: var(--ink); font-size: .56rem; }
.catalog-group-tags { display: flex; min-width: 0; flex-wrap: wrap; gap: .28rem; margin-top: .38rem; }
.catalog-group-tags span { max-width: 100%; padding: .18rem .36rem; border: 1px solid var(--line); border-radius: 999px; color: var(--ink-soft); font-size: .55rem; white-space: nowrap; }
.catalog-group-tags span.is-ready { border-color: color-mix(in srgb, rgb(var(--dd-signal-green)) 35%, var(--line)); background: color-mix(in srgb, rgb(var(--dd-signal-green)) 8%, var(--surface)); color: rgb(var(--dd-signal-green)); }
.catalog-group-tags em { color: var(--ink-faint); font-size: .58rem; font-style: normal; }
.catalog-card-action { display: flex; min-height: 2.8rem; align-items: center; justify-content: space-between; padding: 0 .85rem; border-top: 1px solid var(--line); color: var(--ink); font-size: .68rem; font-weight: 800; }
.catalog-card-action:hover { background: var(--surface-muted); }
.catalog-card-action span { font-size: .9rem; transition: transform .16s ease; }
.catalog-card-action:hover span { transform: translateX(3px); }
.catalog-loading { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr)); gap: .85rem; }
.catalog-loading span { min-height: 28rem; border-radius: .86rem; background: linear-gradient(100deg, var(--surface-muted), var(--surface), var(--surface-muted)); background-size: 220% 100%; animation: catalog-loading 1.2s linear infinite; }
.catalog-empty { display: grid; min-height: 18rem; place-items: center; align-content: center; gap: .35rem; border: 1px solid var(--line); border-radius: .85rem; background: var(--surface); color: var(--ink-soft); font-size: .7rem; }
.catalog-empty strong { color: var(--ink); font-size: .8rem; }
.catalog-toolbar :is(button, input, select):focus-visible, .catalog-card-action:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
:global(:root[data-theme='dark']) .catalog-card { --provider-bg: #2d322f; --provider-ink: #e1ece5; }
:global(:root[data-theme='dark']) .catalog-card.is-anthropic { --provider-bg: #3a2924; --provider-ink: #f0a58e; }
:global(:root[data-theme='dark']) .catalog-card.is-gemini { --provider-bg: #29283c; --provider-ink: #c1bcff; }
:global(:root[data-theme='dark']) .catalog-card.is-grok { --provider-bg: #2b2b2d; --provider-ink: #f0f0f1; }
:global(:root[data-theme='dark']) .catalog-card.is-kimi { --provider-bg: #25303a; --provider-ink: #d6e8f7; }
:global(:root[data-theme='dark']) .catalog-card.is-zhipu, :global(:root[data-theme='dark']) .catalog-card.is-deepseek { --provider-bg: #252d43; --provider-ink: #aebfff; }
@keyframes catalog-loading { to { background-position: -220% 0; } }
@media (max-width: 760px) {
  .public-models-heading { align-items: flex-start; flex-direction: column; }
  .public-models-summary { width: 100%; grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .public-models-summary > div { min-width: 0; }
  .catalog-fields { grid-template-columns: 1fr 1.5fr; }
}
@media (max-width: 480px) {
  .catalog-toolbar { padding: .5rem; }
  .catalog-provider-tabs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .catalog-provider-tabs button { width: 100%; min-width: 0; justify-content: flex-start; overflow: hidden; }
  .catalog-provider-tabs button b { margin-left: auto; }
  .catalog-fields { grid-template-columns: 1fr; }
  .catalog-grid { grid-template-columns: 1fr; }
}
@media (prefers-reduced-motion: reduce) { .catalog-card, .catalog-card-action span { transition: none; } .catalog-card:hover, .catalog-card-action:hover span { transform: none; } .catalog-loading span { animation: none; } }
</style>
