<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { localizedApiError, localizeErrorMessage } from '../api/errors'

interface ImageResponseItem {
  b64_json?: string
  url?: string
  revised_prompt?: string
}

interface StudioImage {
  id: string
  src: string
  prompt: string
  revisedPrompt: string
  createdAt: Date
}

const studioKeyStorage = 'dengdeng.image-studio.api-key'
const apiKey = ref('')
const revealKey = ref(false)
const prompt = ref('')
const model = ref('gpt-image-2')
const size = ref('1024x1024')
const quality = ref('medium')
const generating = ref(false)
const error = ref('')
const gallery = ref<StudioImage[]>([])
const selectedImageID = ref('')

const sizeOptions = [
  { id: '1024x1024', label: '方形', dimensions: '1024 × 1024', shape: 'square' },
  { id: '1536x1024', label: '横幅', dimensions: '1536 × 1024', shape: 'landscape' },
  { id: '1024x1536', label: '竖幅', dimensions: '1024 × 1536', shape: 'portrait' },
]

const qualityOptions = [
  { id: 'low', label: '快速' },
  { id: 'medium', label: '标准' },
  { id: 'high', label: '精细' },
]

const promptSuggestions = [
  '雨后傍晚的城市街角，暖色橱窗映在潮湿路面，35mm 胶片质感',
  '一件放在深色石材上的陶瓷香氛产品，柔和侧光，克制的商业静物摄影',
  '山间小屋的早餐桌，窗外有云雾和松林，细节丰富的生活方式杂志摄影',
]

const hasKey = computed(() => apiKey.value.trim().startsWith('dd-'))
const canGenerate = computed(() => hasKey.value && Boolean(prompt.value.trim()) && !generating.value)
const selectedImage = computed(() => gallery.value.find((item) => item.id === selectedImageID.value) || gallery.value[0] || null)
const selectedSize = computed(() => sizeOptions.find((item) => item.id === size.value) || sizeOptions[0])
const keyStatus = computed(() => hasKey.value ? '已在本标签页就绪' : '粘贴 dd- 密钥后开始')

function readSessionKey() {
  try {
    return sessionStorage.getItem(studioKeyStorage) || ''
  } catch {
    return ''
  }
}

function persistSessionKey(value: string) {
  try {
    if (value.trim()) sessionStorage.setItem(studioKeyStorage, value.trim())
    else sessionStorage.removeItem(studioKeyStorage)
  } catch {
    // Private browsing can deny storage; the key remains available for this view.
  }
}

function useSuggestion(value: string) {
  prompt.value = value
}

function clearKey() {
  apiKey.value = ''
  revealKey.value = false
}

function responseError(payload: unknown, status: number) {
	return localizedApiError(status, payload)
}

function imageSource(item: ImageResponseItem) {
  if (item.b64_json) return `data:image/png;base64,${item.b64_json}`
  return item.url || ''
}

async function generate() {
  if (!canGenerate.value) return
  generating.value = true
  error.value = ''
  const requestedPrompt = prompt.value.trim()
  try {
    const response = await fetch('/v1/images/generations', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${apiKey.value.trim()}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        model: model.value,
        prompt: requestedPrompt,
        size: size.value,
        quality: quality.value,
        background: 'auto',
        output_format: 'png',
        n: 1,
      }),
    })
    const payload = await response.json().catch(() => null) as { data?: ImageResponseItem[] } | null
    if (!response.ok) throw new Error(responseError(payload, response.status))
    const incoming = Array.isArray(payload?.data) ? payload.data : []
    const created = incoming.map((item, index) => ({
      id: `${Date.now()}-${index}`,
      src: imageSource(item),
      prompt: requestedPrompt,
      revisedPrompt: item.revised_prompt || '',
      createdAt: new Date(),
    })).filter((item) => item.src)
    if (!created.length) throw new Error('接口没有返回可展示的图像')
    gallery.value = [...created, ...gallery.value]
    selectedImageID.value = created[0].id
  } catch (cause) {
    const message = cause instanceof Error ? cause.message : '生成失败，请稍后再试'
    error.value = message.includes('401') || /unauthorized|invalid api key|密钥/i.test(message)
      ? '密钥无效或已失效，请重新粘贴后再试。'
		: localizeErrorMessage(message)
  } finally {
    generating.value = false
  }
}

function downloadSelected() {
  const item = selectedImage.value
  if (!item) return
  const link = document.createElement('a')
  link.href = item.src
  link.download = `dengdeng-${item.id}.png`
  document.body.appendChild(link)
  link.click()
  link.remove()
}

function formatTime(value: Date) {
  return value.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

onMounted(() => {
  apiKey.value = readSessionKey()
})

watch(apiKey, persistSessionKey)
</script>

<template>
  <div class="image-studio">
    <header class="studio-topbar">
      <RouterLink to="/studio" class="studio-brand" aria-label="DengDeng 图像创作首页">
        <span class="studio-brand-mark" aria-hidden="true">DD</span>
        <span><b>DengDeng</b><em>图像创作</em></span>
      </RouterLink>
      <nav class="studio-topbar-actions" aria-label="页面导航">
        <RouterLink to="/login">登录管理</RouterLink>
        <a href="#create">开始创作</a>
      </nav>
    </header>

    <main class="studio-main" id="create">
      <section class="studio-intro" aria-labelledby="studio-title">
        <div>
          <p class="studio-kicker">DengDeng Canvas</p>
          <h1 id="studio-title">把想法落成一张图。</h1>
        </div>
        <p>输入描述，选好画幅，直接通过 DengDeng API 生成。作品只保留在当前会话。</p>
      </section>

      <section class="studio-keybar" aria-label="API 密钥">
        <div class="studio-keybar-copy"><strong>API 密钥</strong><span>{{ keyStatus }}</span></div>
        <div class="studio-keybar-field">
          <input v-model="apiKey" :type="revealKey ? 'text' : 'password'" autocomplete="off" autocapitalize="none" spellcheck="false" placeholder="dd-…" aria-label="DengDeng API 密钥" />
          <button type="button" :aria-label="revealKey ? '隐藏密钥' : '显示密钥'" @click="revealKey = !revealKey">{{ revealKey ? '隐藏' : '显示' }}</button>
          <button v-if="apiKey" type="button" class="studio-key-clear" @click="clearKey">清除</button>
        </div>
        <p>密钥仅保留在当前浏览器标签页。</p>
      </section>

      <div class="studio-workspace">
        <section class="studio-composer" aria-labelledby="composer-title">
          <div class="studio-section-head">
            <div><p>创作面板</p><h2 id="composer-title">描述画面</h2></div>
            <span class="studio-model-badge">{{ model }}</span>
          </div>

          <label class="studio-prompt-field">
            <span>提示词</span>
            <textarea v-model="prompt" rows="7" placeholder="例如：一束干燥野花放在胡桃木桌面，午后侧光，静物摄影，细节清晰。"></textarea>
          </label>

          <div class="studio-suggestions" aria-label="提示词示例">
            <span>试试</span>
            <button v-for="suggestion in promptSuggestions" :key="suggestion" type="button" @click="useSuggestion(suggestion)">{{ suggestion }}</button>
          </div>

          <div class="studio-control-grid">
            <fieldset class="studio-control-group">
              <legend>画幅</legend>
              <div class="studio-size-options">
                <button v-for="option in sizeOptions" :key="option.id" type="button" :class="{ 'is-active': size === option.id }" @click="size = option.id">
                  <i class="studio-size-glyph" :class="`is-${option.shape}`" aria-hidden="true"></i>
                  <span>{{ option.label }}</span><small>{{ option.dimensions }}</small>
                </button>
              </div>
            </fieldset>

            <fieldset class="studio-control-group">
              <legend>细节强度</legend>
              <div class="studio-quality-options">
                <button v-for="option in qualityOptions" :key="option.id" type="button" :class="{ 'is-active': quality === option.id }" @click="quality = option.id">{{ option.label }}</button>
              </div>
              <p>{{ quality === 'low' ? '更快出草图' : quality === 'high' ? '适合最终稿' : '速度与细节平衡' }}</p>
            </fieldset>
          </div>

          <div class="studio-submit-row">
            <span v-if="error" class="studio-error" role="alert">{{ error }}</span>
            <span v-else class="studio-api-note">PNG · {{ selectedSize.dimensions }} · 单张生成</span>
            <button type="button" class="studio-generate" :disabled="!canGenerate" @click="generate">
              <span v-if="generating" class="studio-button-loader" aria-hidden="true"></span>
              {{ generating ? '正在生成' : '生成图像' }}
            </button>
          </div>
        </section>

        <section class="studio-output" aria-labelledby="output-title" :class="{ 'is-generating': generating }">
          <div class="studio-output-head"><div><p>作品区</p><h2 id="output-title">{{ selectedImage ? '最新作品' : '等待第一张作品' }}</h2></div><span v-if="selectedImage">{{ formatTime(selectedImage.createdAt) }}</span></div>

          <div v-if="selectedImage" class="studio-canvas-wrap">
            <img :src="selectedImage.src" :alt="selectedImage.revisedPrompt || selectedImage.prompt" class="studio-canvas-image" />
            <div class="studio-canvas-actions"><button type="button" @click="downloadSelected">下载 PNG</button></div>
          </div>
          <div v-else class="studio-empty-canvas">
            <div class="studio-empty-frame" aria-hidden="true"><span></span><i></i></div>
            <p>{{ generating ? '正在把描述转成画面…' : '第一张作品会出现在这里。' }}</p>
          </div>

          <div v-if="gallery.length > 1" class="studio-gallery" aria-label="本次会话作品">
            <button v-for="item in gallery" :key="item.id" type="button" :class="{ 'is-selected': item.id === selectedImage?.id }" @click="selectedImageID = item.id"><img :src="item.src" :alt="item.prompt" /></button>
          </div>
          <p v-if="selectedImage?.revisedPrompt" class="studio-revised-prompt">{{ selectedImage.revisedPrompt }}</p>
        </section>
      </div>
    </main>
  </div>
</template>
