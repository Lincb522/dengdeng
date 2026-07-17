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
  { id: '1024x1024', shape: 'square', label: '方形 1024 × 1024' },
  { id: '1536x1024', shape: 'landscape', label: '横幅 1536 × 1024' },
  { id: '1024x1536', shape: 'portrait', label: '竖幅 1024 × 1536' },
]

const qualityOptions = [
  { id: 'low', label: '低' },
  { id: 'medium', label: '中' },
  { id: 'high', label: '高' },
]

const hasKey = computed(() => apiKey.value.trim().startsWith('dd-'))
const canGenerate = computed(() => hasKey.value && Boolean(prompt.value.trim()) && !generating.value)
const selectedImage = computed(() => gallery.value.find((item) => item.id === selectedImageID.value) || gallery.value[0] || null)

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

onMounted(() => {
  apiKey.value = readSessionKey()
})

watch(apiKey, persistSessionKey)
</script>

<template>
  <div class="image-studio">
    <header class="studio-topbar">
      <RouterLink to="/studio" class="studio-brand" aria-label="DengDeng 图像创作">
        <img src="/brand/dengdeng-avatar.png" alt="" />
      </RouterLink>
      <RouterLink to="/login" class="studio-icon-button" aria-label="登录管理" title="登录管理">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12h13M13 6l6 6-6 6" /></svg>
      </RouterLink>
    </header>

    <main class="studio-main">
      <h1 class="sr-only">DengDeng 图像创作</h1>

      <section class="studio-keybar" aria-label="API 密钥">
        <span class="studio-key-glyph" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M14.5 9.5a4.5 4.5 0 1 0-4.1 5.1L13 12h2l1.5-1.5H19v-2h-2.5L15 10z" /></svg>
        </span>
        <input v-model="apiKey" :type="revealKey ? 'text' : 'password'" autocomplete="off" autocapitalize="none" spellcheck="false" placeholder="dd-…" aria-label="DengDeng API 密钥" />
        <button type="button" class="studio-key-action" :aria-label="revealKey ? '隐藏密钥' : '显示密钥'" :title="revealKey ? '隐藏密钥' : '显示密钥'" @click="revealKey = !revealKey">
          <svg v-if="revealKey" viewBox="0 0 24 24" aria-hidden="true"><path d="m3 3 18 18M10.6 10.7a2 2 0 0 0 2.7 2.7M9.9 5.2A10.6 10.6 0 0 1 12 5c5.3 0 9 4.4 9 7s-1.3 3.6-3.1 5M6.2 6.2C4.2 7.6 3 9.8 3 12c0 2.6 3.7 7 9 7 1.2 0 2.3-.2 3.3-.6" /></svg>
          <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12s3.2-7 9-7 9 7 9 7-3.2 7-9 7-9-7-9-7Z" /><circle cx="12" cy="12" r="2.5" /></svg>
        </button>
        <button v-if="apiKey" type="button" class="studio-key-action is-danger" aria-label="清除密钥" title="清除密钥" @click="clearKey">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
        </button>
      </section>

      <section class="studio-workspace" aria-label="图像生成工作台">
        <div class="studio-composer">
          <textarea v-model="prompt" rows="8" placeholder="描述画面" aria-label="提示词" @keydown.meta.enter.prevent="generate" @keydown.ctrl.enter.prevent="generate"></textarea>

          <div class="studio-controls">
            <div class="studio-size-options" role="group" aria-label="画幅">
              <button v-for="option in sizeOptions" :key="option.id" type="button" :class="{ 'is-active': size === option.id }" :aria-label="option.label" :title="option.label" @click="size = option.id">
                <i class="studio-size-glyph" :class="`is-${option.shape}`" aria-hidden="true"></i>
              </button>
            </div>
            <div class="studio-quality-options" role="group" aria-label="生成质量">
              <button v-for="option in qualityOptions" :key="option.id" type="button" :class="{ 'is-active': quality === option.id }" :aria-label="`${option.label}质量`" @click="quality = option.id">{{ option.label }}</button>
            </div>
          </div>

          <div class="studio-submit-row">
            <p v-if="error" class="studio-error" role="alert">{{ error }}</p>
            <button type="button" class="studio-generate" :disabled="!canGenerate" @click="generate">
              <span v-if="generating" class="studio-button-loader" aria-hidden="true"></span>
              {{ generating ? '生成中' : '生成' }}
            </button>
          </div>
        </div>

        <div class="studio-output" :class="{ 'is-generating': generating }" aria-live="polite">
          <div v-if="selectedImage" class="studio-canvas-wrap">
            <img :src="selectedImage.src" :alt="selectedImage.revisedPrompt || selectedImage.prompt" class="studio-canvas-image" />
            <button type="button" class="studio-download" aria-label="下载 PNG" title="下载 PNG" @click="downloadSelected">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v12m0 0 4-4m-4 4-4-4M5 20h14" /></svg>
            </button>
          </div>
          <div v-else class="studio-empty-canvas" aria-hidden="true"><span></span></div>

          <div v-if="gallery.length > 1" class="studio-gallery" aria-label="本次会话作品">
            <button v-for="item in gallery" :key="item.id" type="button" :class="{ 'is-selected': item.id === selectedImage?.id }" :aria-label="item.prompt" @click="selectedImageID = item.id"><img :src="item.src" alt="" /></button>
          </div>
        </div>
      </section>
    </main>
  </div>
</template>
