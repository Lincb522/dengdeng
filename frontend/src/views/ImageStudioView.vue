<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { localizedApiError, localizeErrorMessage } from '../api/errors'

interface ImageResponseItem {
  b64_json?: string
  url?: string
  revised_prompt?: string
}

type StudioTool = 'select' | 'hand' | 'note'
type StudioNodeKind = 'image' | 'note'
type StudioNodeStatus = 'ready' | 'generating' | 'error'

interface StudioNode {
  id: string
  kind: StudioNodeKind
  x: number
  y: number
  width: number
  height: number
  src?: string
  prompt: string
  revisedPrompt?: string
  status: StudioNodeStatus
  error?: string
}

interface PointerSession {
  mode: 'pan' | 'drag'
  pointerId: number
  startX: number
  startY: number
  originX: number
  originY: number
  nodeID?: string
}

const studioKeyStorage = 'dengdeng.image-studio.api-key'
const viewportElement = ref<HTMLElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const apiKey = ref('')
const revealKey = ref(false)
const prompt = ref('')
const model = ref('gpt-image-2')
const size = ref('1024x1024')
const quality = ref('medium')
const generating = ref(false)
const error = ref('')
const nodes = ref<StudioNode[]>([])
const selectedNodeID = ref('')
const tool = ref<StudioTool>('select')
const spacePressed = ref(false)
const viewport = ref({ x: 320, y: 180, scale: 0.9 })
const pointerSession = ref<PointerSession | null>(null)
const objectURLs = new Set<string>()

const sizeOptions = [
  { id: '1024x1024', shape: 'square', label: '方形' },
  { id: '1536x1024', shape: 'landscape', label: '横幅' },
  { id: '1024x1536', shape: 'portrait', label: '竖幅' },
]

const qualityOptions = [
  { id: 'low', label: '低' },
  { id: 'medium', label: '中' },
  { id: 'high', label: '高' },
]

const hasKey = computed(() => apiKey.value.trim().startsWith('dd-'))
const canGenerate = computed(() => hasKey.value && Boolean(prompt.value.trim()) && !generating.value)
const selectedNode = computed(() => nodes.value.find((item) => item.id === selectedNodeID.value) || null)
const canvasTransform = computed(() => ({
  transform: `translate3d(${viewport.value.x}px, ${viewport.value.y}px, 0) scale(${viewport.value.scale})`,
}))
const canvasBackground = computed(() => ({
  '--studio-grid-size': `${24 * viewport.value.scale}px`,
  '--studio-grid-x': `${viewport.value.x}px`,
  '--studio-grid-y': `${viewport.value.y}px`,
} as Record<string, string>))
const zoomLabel = computed(() => `${Math.round(viewport.value.scale * 100)}%`)

function readSessionKey() {
  try { return sessionStorage.getItem(studioKeyStorage) || '' } catch { return '' }
}

function persistSessionKey(value: string) {
  try {
    if (value.trim()) sessionStorage.setItem(studioKeyStorage, value.trim())
    else sessionStorage.removeItem(studioKeyStorage)
  } catch { /* The key remains available for this view. */ }
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

function sizeDimensions(value = size.value) {
  const [rawWidth, rawHeight] = value.split('x').map(Number)
  const ratio = rawWidth > 0 && rawHeight > 0 ? rawHeight / rawWidth : 1
  const width = ratio > 1.2 ? 320 : 420
  return { width, height: Math.round(width * ratio) }
}

function screenToWorld(clientX: number, clientY: number) {
  const rect = viewportElement.value?.getBoundingClientRect()
  if (!rect) return { x: 0, y: 0 }
  return {
    x: (clientX - rect.left - viewport.value.x) / viewport.value.scale,
    y: (clientY - rect.top - viewport.value.y) / viewport.value.scale,
  }
}

function canvasCenter() {
  const rect = viewportElement.value?.getBoundingClientRect()
  if (!rect) return { x: 0, y: 0 }
  return screenToWorld(rect.left + rect.width / 2, rect.top + rect.height / 2)
}

function nextNodePosition(width: number, height: number) {
  const center = canvasCenter()
  const offset = (nodes.value.length % 6) * 28
  return { x: center.x - width / 2 + offset, y: center.y - height / 2 + offset }
}

function createID(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
}

function clampZoom(value: number) {
  return Math.min(2.5, Math.max(0.2, value))
}

function zoomAt(nextScale: number, clientX?: number, clientY?: number) {
  const rect = viewportElement.value?.getBoundingClientRect()
  if (!rect) return
  const sx = clientX === undefined ? rect.width / 2 : clientX - rect.left
  const sy = clientY === undefined ? rect.height / 2 : clientY - rect.top
  const worldX = (sx - viewport.value.x) / viewport.value.scale
  const worldY = (sy - viewport.value.y) / viewport.value.scale
  const scale = clampZoom(nextScale)
  viewport.value = { x: sx - worldX * scale, y: sy - worldY * scale, scale }
}

function zoomBy(factor: number) {
  zoomAt(viewport.value.scale * factor)
}

function resetView() {
  if (!nodes.value.length) {
    const rect = viewportElement.value?.getBoundingClientRect()
    viewport.value = { x: rect ? rect.width / 2 : 320, y: rect ? rect.height / 2 : 180, scale: 1 }
    return
  }
  const minX = Math.min(...nodes.value.map((node) => node.x))
  const minY = Math.min(...nodes.value.map((node) => node.y))
  const maxX = Math.max(...nodes.value.map((node) => node.x + node.width))
  const maxY = Math.max(...nodes.value.map((node) => node.y + node.height))
  const rect = viewportElement.value?.getBoundingClientRect()
  if (!rect) return
  const padding = Math.min(140, rect.width * 0.12)
  const scale = clampZoom(Math.min((rect.width - padding * 2) / Math.max(maxX - minX, 1), (rect.height - padding * 2) / Math.max(maxY - minY, 1), 1.35))
  viewport.value = {
    x: (rect.width - (maxX - minX) * scale) / 2 - minX * scale,
    y: (rect.height - (maxY - minY) * scale) / 2 - minY * scale,
    scale,
  }
}

function onWheel(event: WheelEvent) {
  if (event.ctrlKey || event.metaKey) {
    event.preventDefault()
    zoomAt(viewport.value.scale * Math.exp(-event.deltaY * 0.002), event.clientX, event.clientY)
    return
  }
  viewport.value = { ...viewport.value, x: viewport.value.x - event.deltaX, y: viewport.value.y - event.deltaY }
}

function startCanvasPointer(event: PointerEvent) {
  if (event.button !== 0 && event.button !== 1) return
  if (tool.value === 'note' && event.button === 0) {
    const point = screenToWorld(event.clientX, event.clientY)
    const node: StudioNode = { id: createID('note'), kind: 'note', x: point.x - 110, y: point.y - 70, width: 220, height: 140, prompt: '', status: 'ready' }
    nodes.value.push(node)
    selectedNodeID.value = node.id
    tool.value = 'select'
    return
  }
  selectedNodeID.value = ''
  pointerSession.value = { mode: 'pan', pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, originX: viewport.value.x, originY: viewport.value.y }
  viewportElement.value?.setPointerCapture(event.pointerId)
}

function startNodePointer(event: PointerEvent, node: StudioNode) {
  if (event.button !== 0) return
  event.stopPropagation()
  selectedNodeID.value = node.id
  if (tool.value === 'hand' || spacePressed.value) {
    pointerSession.value = { mode: 'pan', pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, originX: viewport.value.x, originY: viewport.value.y }
  } else {
    pointerSession.value = { mode: 'drag', pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, originX: node.x, originY: node.y, nodeID: node.id }
  }
  viewportElement.value?.setPointerCapture(event.pointerId)
}

function movePointer(event: PointerEvent) {
  const session = pointerSession.value
  if (!session || session.pointerId !== event.pointerId) return
  if (session.mode === 'pan') {
    viewport.value = { ...viewport.value, x: session.originX + event.clientX - session.startX, y: session.originY + event.clientY - session.startY }
    return
  }
  const node = nodes.value.find((item) => item.id === session.nodeID)
  if (!node) return
  node.x = session.originX + (event.clientX - session.startX) / viewport.value.scale
  node.y = session.originY + (event.clientY - session.startY) / viewport.value.scale
}

function endPointer(event: PointerEvent) {
  if (pointerSession.value?.pointerId !== event.pointerId) return
  pointerSession.value = null
  if (viewportElement.value?.hasPointerCapture(event.pointerId)) viewportElement.value.releasePointerCapture(event.pointerId)
}

function addNote() {
  tool.value = 'note'
}

function removeNode(id = selectedNodeID.value) {
  const index = nodes.value.findIndex((item) => item.id === id)
  if (index < 0) return
  const [removed] = nodes.value.splice(index, 1)
  if (removed.src?.startsWith('blob:')) {
    URL.revokeObjectURL(removed.src)
    objectURLs.delete(removed.src)
  }
  if (selectedNodeID.value === id) selectedNodeID.value = ''
}

function duplicateNode(node = selectedNode.value) {
  if (!node) return
  const copy = { ...node, id: createID(node.kind), x: node.x + 34, y: node.y + 34 }
  nodes.value.push(copy)
  selectedNodeID.value = copy.id
}

function downloadNode(node = selectedNode.value) {
  if (!node?.src) return
  const link = document.createElement('a')
  link.href = node.src
  link.download = `dengdeng-${node.id}.png`
  document.body.appendChild(link)
  link.click()
  link.remove()
}

function importFiles(files: FileList | File[]) {
  Array.from(files).filter((file) => file.type.startsWith('image/')).forEach((file, index) => {
    const src = URL.createObjectURL(file)
    objectURLs.add(src)
    const dimensions = { width: 400, height: 300 }
    const position = nextNodePosition(dimensions.width, dimensions.height)
    const node: StudioNode = { id: createID('image'), kind: 'image', ...position, ...dimensions, src, prompt: file.name, status: 'ready' }
    nodes.value.push(node)
    selectedNodeID.value = node.id
    const image = new Image()
    image.onload = () => {
      const maxSide = 440
      const ratio = Math.min(maxSide / image.naturalWidth, maxSide / image.naturalHeight, 1)
      node.width = Math.max(160, Math.round(image.naturalWidth * ratio))
      node.height = Math.max(120, Math.round(image.naturalHeight * ratio))
      node.x += index * 24
      node.y += index * 24
    }
    image.src = src
  })
  if (fileInput.value) fileInput.value.value = ''
}

function onDrop(event: DragEvent) {
  if (event.dataTransfer?.files.length) importFiles(event.dataTransfer.files)
}

async function generate() {
  if (!canGenerate.value) return
  generating.value = true
  error.value = ''
  const requestedPrompt = prompt.value.trim()
  const dimensions = sizeDimensions()
  const position = nextNodePosition(dimensions.width, dimensions.height)
  const placeholder: StudioNode = { id: createID('generation'), kind: 'image', ...position, ...dimensions, prompt: requestedPrompt, status: 'generating' }
  nodes.value.push(placeholder)
  selectedNodeID.value = placeholder.id
  try {
    const response = await fetch('/v1/images/generations', {
      method: 'POST',
      headers: { Authorization: `Bearer ${apiKey.value.trim()}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: model.value, prompt: requestedPrompt, size: size.value, quality: quality.value, background: 'auto', output_format: 'png', n: 1 }),
    })
    const payload = await response.json().catch(() => null) as { data?: ImageResponseItem[] } | null
    if (!response.ok) throw new Error(responseError(payload, response.status))
    const incoming = Array.isArray(payload?.data) ? payload.data.filter((item) => imageSource(item)) : []
    if (!incoming.length) throw new Error('接口没有返回可展示的图像')
    placeholder.src = imageSource(incoming[0])
    placeholder.revisedPrompt = incoming[0].revised_prompt || ''
    placeholder.status = 'ready'
    incoming.slice(1).forEach((item, index) => nodes.value.push({
      ...placeholder,
      id: createID('generation'),
      x: placeholder.x + (index + 1) * 36,
      y: placeholder.y + (index + 1) * 36,
      src: imageSource(item),
      revisedPrompt: item.revised_prompt || '',
    }))
  } catch (cause) {
    const message = cause instanceof Error ? cause.message : '生成失败，请稍后再试'
    const localized = message.includes('401') || /unauthorized|invalid api key|密钥/i.test(message)
      ? '密钥无效或已失效，请重新粘贴后再试。'
      : localizeErrorMessage(message)
    error.value = localized
    placeholder.status = 'error'
    placeholder.error = localized
  } finally {
    generating.value = false
  }
}

function clearCanvas() {
  if (!nodes.value.length || !window.confirm('清空当前画布？')) return
  nodes.value.forEach((node) => {
    if (node.src?.startsWith('blob:')) URL.revokeObjectURL(node.src)
  })
  objectURLs.clear()
  nodes.value = []
  selectedNodeID.value = ''
}

function onKeyDown(event: KeyboardEvent) {
  const target = event.target as HTMLElement | null
  const editing = target?.matches('input, textarea, [contenteditable="true"]')
  if (event.code === 'Space' && !editing) {
    spacePressed.value = true
    event.preventDefault()
  }
  if (editing) return
  if ((event.key === 'Delete' || event.key === 'Backspace') && selectedNodeID.value) removeNode()
  if ((event.metaKey || event.ctrlKey) && event.key === '0') { event.preventDefault(); resetView() }
  if (event.key === '+' || event.key === '=') zoomBy(1.15)
  if (event.key === '-') zoomBy(0.87)
  if (event.key.toLowerCase() === 'v') tool.value = 'select'
  if (event.key.toLowerCase() === 'h') tool.value = 'hand'
  if (event.key.toLowerCase() === 'n') tool.value = 'note'
}

function onKeyUp(event: KeyboardEvent) {
  if (event.code === 'Space') spacePressed.value = false
}

onMounted(async () => {
  apiKey.value = readSessionKey()
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('keyup', onKeyUp)
  await nextTick()
  resetView()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('keyup', onKeyUp)
  objectURLs.forEach((url) => URL.revokeObjectURL(url))
})

watch(apiKey, persistSessionKey)
</script>

<template>
  <div class="image-studio image-studio--canvas">
    <header class="studio-canvas-topbar">
      <RouterLink to="/studio" class="studio-brand" aria-label="DengDeng 图像制作">
        <img src="/brand/dengdeng-avatar.png" alt="" />
        <span>图像制作</span>
      </RouterLink>
      <div class="studio-document-title">
        <strong>未命名画布</strong>
        <span>{{ nodes.length }} 个对象</span>
      </div>
      <div class="studio-topbar-actions">
        <button type="button" :disabled="!nodes.length" @click="clearCanvas">清空</button>
        <RouterLink to="/login">控制台</RouterLink>
      </div>
    </header>

    <main class="studio-canvas-shell">
      <aside class="studio-tool-rail" aria-label="画布工具">
        <button type="button" :class="{ 'is-active': tool === 'select' }" aria-label="选择工具" title="选择 V" @click="tool = 'select'">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 3 13.5 8.2-6.2 1.2-3.1 5.4L5 3Z" /></svg>
        </button>
        <button type="button" :class="{ 'is-active': tool === 'hand' }" aria-label="抓手工具" title="抓手 H" @click="tool = 'hand'">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7.5 11V7.2a1.5 1.5 0 0 1 3 0V10m0-3.8a1.5 1.5 0 0 1 3 0V10m0-2.8a1.5 1.5 0 0 1 3 0v4m0-2a1.5 1.5 0 0 1 3 0v4.2c0 4.2-2.5 6.6-6.5 6.6h-1.1a6 6 0 0 1-4.3-1.8L4.8 15a1.7 1.7 0 0 1 2.4-2.4l1.3 1.1" /></svg>
        </button>
        <button type="button" :class="{ 'is-active': tool === 'note' }" aria-label="添加便签" title="便签 N" @click="addNote">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 4h14v12l-4 4H5V4Z" /><path d="M15 20v-4h4M8 8h8M8 12h5" /></svg>
        </button>
        <button type="button" aria-label="导入图片" title="导入图片" @click="fileInput?.click()">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 5h16v14H4z" /><circle cx="9" cy="10" r="1.5" /><path d="m5 17 4.5-4 3 2.5 2.5-2 4 3.5" /></svg>
        </button>
        <span class="studio-tool-divider"></span>
        <button type="button" aria-label="适配画布" title="适配画布 ⌘0" @click="resetView">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 3H3v5M16 3h5v5M8 21H3v-5M16 21h5v-5" /></svg>
        </button>
        <input ref="fileInput" class="sr-only" type="file" accept="image/*" multiple @change="($event.target as HTMLInputElement).files && importFiles(($event.target as HTMLInputElement).files!)" />
      </aside>

      <section
        ref="viewportElement"
        class="studio-infinite-canvas"
        :class="[`is-${tool}`, { 'is-panning': pointerSession?.mode === 'pan' || spacePressed }]"
        :style="canvasBackground"
        aria-label="无限画布"
        @pointerdown="startCanvasPointer"
        @pointermove="movePointer"
        @pointerup="endPointer"
        @pointercancel="endPointer"
        @wheel="onWheel"
        @dragover.prevent
        @drop.prevent="onDrop"
      >
        <div v-if="!nodes.length" class="studio-canvas-empty" aria-hidden="true">
          <span>双指移动画布 · ⌘/Ctrl + 滚轮缩放</span>
        </div>

        <div class="studio-canvas-world" :style="canvasTransform">
          <article
            v-for="node in nodes"
            :key="node.id"
            class="studio-canvas-node"
            :class="[`is-${node.kind}`, `is-${node.status}`, { 'is-selected': selectedNodeID === node.id }]"
            :style="{ left: `${node.x}px`, top: `${node.y}px`, width: `${node.width}px`, height: `${node.height}px` }"
            @pointerdown="startNodePointer($event, node)"
          >
            <template v-if="node.kind === 'image'">
              <img v-if="node.src" :src="node.src" :alt="node.revisedPrompt || node.prompt" draggable="false" />
              <div v-else-if="node.status === 'generating'" class="studio-node-loading"><span></span><p>正在生成</p></div>
              <div v-else class="studio-node-error"><strong>生成失败</strong><p>{{ node.error }}</p></div>
            </template>
            <textarea v-else v-model="node.prompt" placeholder="写点什么" aria-label="便签内容" @pointerdown.stop></textarea>

            <div v-if="selectedNodeID === node.id" class="studio-node-actions" @pointerdown.stop>
              <button v-if="node.src" type="button" aria-label="下载" title="下载" @click="downloadNode(node)">
                <svg viewBox="0 0 24 24"><path d="M12 3v12m0 0 4-4m-4 4-4-4M5 20h14" /></svg>
              </button>
              <button type="button" aria-label="复制" title="复制" @click="duplicateNode(node)">
                <svg viewBox="0 0 24 24"><rect x="8" y="8" width="11" height="11" rx="1" /><path d="M16 8V5H5v11h3" /></svg>
              </button>
              <button type="button" aria-label="删除" title="删除" @click="removeNode(node.id)">
                <svg viewBox="0 0 24 24"><path d="M5 7h14M9 7V4h6v3M8 10v8m4-8v8m4-8v8M7 7l1 14h8l1-14" /></svg>
              </button>
            </div>
          </article>
        </div>

        <div class="studio-zoom-control" @pointerdown.stop>
          <button type="button" aria-label="缩小" @click="zoomBy(.85)">−</button>
          <button type="button" class="studio-zoom-value" aria-label="适配画布" @click="resetView">{{ zoomLabel }}</button>
          <button type="button" aria-label="放大" @click="zoomBy(1.15)">＋</button>
        </div>
      </section>

      <aside class="studio-generator-panel" aria-label="生成设置">
        <div class="studio-generator-head">
          <div><strong>生成</strong><span>⌘ Enter</span></div>
          <span class="studio-key-status" :class="{ 'is-ready': hasKey }">{{ hasKey ? '密钥已就绪' : '需要密钥' }}</span>
        </div>

        <label class="studio-generator-field studio-generator-key">
          <span>API 密钥</span>
          <div>
            <input v-model="apiKey" :type="revealKey ? 'text' : 'password'" autocomplete="off" autocapitalize="none" spellcheck="false" placeholder="dd-…" />
            <button type="button" :aria-label="revealKey ? '隐藏密钥' : '显示密钥'" @click="revealKey = !revealKey">
              <svg v-if="revealKey" viewBox="0 0 24 24"><path d="m3 3 18 18M10.6 10.7a2 2 0 0 0 2.7 2.7M6.2 6.2C4.2 7.6 3 9.8 3 12c0 2.6 3.7 7 9 7 1.2 0 2.3-.2 3.3-.6M9.9 5.2A10.6 10.6 0 0 1 12 5c5.3 0 9 4.4 9 7 0 1.8-1.3 3.6-3.1 5" /></svg>
              <svg v-else viewBox="0 0 24 24"><path d="M3 12s3.2-7 9-7 9 7 9 7-3.2 7-9 7-9-7-9-7Z" /><circle cx="12" cy="12" r="2.5" /></svg>
            </button>
            <button v-if="apiKey" type="button" aria-label="清除密钥" @click="clearKey">×</button>
          </div>
        </label>

        <label class="studio-generator-field studio-prompt-field">
          <span>画面描述</span>
          <textarea v-model="prompt" rows="7" placeholder="描述你想生成的画面" @keydown.meta.enter.prevent="generate" @keydown.ctrl.enter.prevent="generate"></textarea>
        </label>

        <label class="studio-generator-field">
          <span>模型</span>
          <input v-model="model" class="studio-model-input" spellcheck="false" />
        </label>

        <div class="studio-generator-field">
          <span>画幅</span>
          <div class="studio-segmented studio-segmented--shape">
            <button v-for="option in sizeOptions" :key="option.id" type="button" :class="{ 'is-active': size === option.id }" :title="option.id" @click="size = option.id">
              <i :class="`is-${option.shape}`"></i>{{ option.label }}
            </button>
          </div>
        </div>

        <div class="studio-generator-field">
          <span>质量</span>
          <div class="studio-segmented">
            <button v-for="option in qualityOptions" :key="option.id" type="button" :class="{ 'is-active': quality === option.id }" @click="quality = option.id">{{ option.label }}</button>
          </div>
        </div>

        <p v-if="error" class="studio-generator-error" role="alert">{{ error }}</p>
        <button type="button" class="studio-generate-button" :disabled="!canGenerate" @click="generate">
          <span v-if="generating" class="studio-button-loader" aria-hidden="true"></span>
          {{ generating ? '正在生成' : '生成到画布' }}
        </button>
      </aside>
    </main>
  </div>
</template>
