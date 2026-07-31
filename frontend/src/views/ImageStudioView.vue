<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Canvas, FabricImage, Group, Point, Rect, Text, Textbox, util, type FabricObject } from 'fabric'
import { localizedApiError, localizeErrorMessage } from '../api/errors'

interface ImageResponseItem {
  b64_json?: string
  url?: string
  revised_prompt?: string
}

type StudioTool = 'select' | 'hand' | 'note'
type StudioObject = FabricObject & { data?: { kind?: string; prompt?: string; revisedPrompt?: string; objectURL?: string } }

const studioKeyStorage = 'dengdeng.image-studio.api-key'
const viewportElement = ref<HTMLElement | null>(null)
const canvasElement = ref<HTMLCanvasElement | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const apiKey = ref('')
const revealKey = ref(false)
const prompt = ref('')
const model = ref('gpt-image-2')
const size = ref('1024x1024')
const quality = ref('medium')
const generating = ref(false)
const error = ref('')
const tool = ref<StudioTool>('select')
const spacePressed = ref(false)
const objectCount = ref(0)
const zoom = ref(1)
const hasSelection = ref(false)
const objectURLs = new Set<string>()
let canvas: Canvas | null = null
let resizeObserver: ResizeObserver | null = null
let isPanning = false
let lastPointer = { x: 0, y: 0 }

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
const zoomLabel = computed(() => `${Math.round(zoom.value * 100)}%`)

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

function imageSource(item: ImageResponseItem) {
  if (item.b64_json) return `data:image/png;base64,${item.b64_json}`
  return item.url || ''
}

function syncCanvasState() {
  if (!canvas) return
  objectCount.value = canvas.getObjects().length
  zoom.value = canvas.getZoom()
  hasSelection.value = Boolean(canvas.getActiveObject())
}

function canvasCenter() {
  if (!canvas) return new Point(0, 0)
  const transform = util.invertTransform(canvas.viewportTransform)
  return new Point(canvas.getWidth() / 2, canvas.getHeight() / 2).transform(transform)
}

function scenePoint(clientX: number, clientY: number) {
  if (!canvas || !viewportElement.value) return new Point(0, 0)
  const rect = viewportElement.value.getBoundingClientRect()
  return new Point(clientX - rect.left, clientY - rect.top).transform(util.invertTransform(canvas.viewportTransform))
}

function sizeDimensions(value = size.value) {
  const [rawWidth, rawHeight] = value.split('x').map(Number)
  const ratio = rawWidth > 0 && rawHeight > 0 ? rawHeight / rawWidth : 1
  const width = ratio > 1.2 ? 320 : 420
  return { width, height: Math.round(width * ratio) }
}

function configureObject(object: StudioObject) {
  object.set({
    borderColor: '#cf8d26',
    cornerColor: '#fffdf9',
    cornerStrokeColor: '#cf8d26',
    cornerStyle: 'circle',
    transparentCorners: false,
    borderScaleFactor: 1.5,
    padding: 2,
  })
  object.setControlsVisibility({ mt: false, mb: false, ml: false, mr: false })
  return object
}

function addToCanvas(object: StudioObject, activate = true) {
  if (!canvas) return
  configureObject(object)
  canvas.add(object)
  if (activate) canvas.setActiveObject(object)
  canvas.requestRenderAll()
  syncCanvasState()
}

function addNoteAt(point = canvasCenter()) {
  if (!canvas) return
  const note = new Textbox('', {
    left: point.x - 110,
    top: point.y - 70,
    width: 220,
    height: 140,
    padding: 16,
    backgroundColor: '#fff3b7',
    fill: '#4b3a18',
    fontFamily: 'system-ui, sans-serif',
    fontSize: 16,
    lineHeight: 1.35,
    splitByGrapheme: true,
  }) as StudioObject
  note.data = { kind: 'note' }
  addToCanvas(note)
  note.enterEditing()
  tool.value = 'select'
}

async function addImage(src: string, metadata: StudioObject['data'] = {}, point = canvasCenter()) {
  if (!canvas) return null
  const image = await FabricImage.fromURL(src, { crossOrigin: src.startsWith('data:') || src.startsWith('blob:') ? undefined : 'anonymous' }) as StudioObject
  const maxSide = 440
  const scale = Math.min(maxSide / Math.max(image.width || 1, image.height || 1), 1)
  image.set({ left: point.x - ((image.width || 1) * scale) / 2, top: point.y - ((image.height || 1) * scale) / 2, scaleX: scale, scaleY: scale })
  image.data = { kind: 'image', ...metadata }
  addToCanvas(image)
  return image
}

function addGeneratingPlaceholder(requestedPrompt: string) {
  const center = canvasCenter()
  const dimensions = sizeDimensions()
  const background = new Rect({ width: dimensions.width, height: dimensions.height, rx: 10, ry: 10, fill: '#eee3d5', stroke: '#decdb8', strokeWidth: 1 })
  const label = new Text('正在生成', { left: dimensions.width / 2, top: dimensions.height / 2, originX: 'center', originY: 'center', fill: '#786957', fontFamily: 'system-ui, sans-serif', fontSize: 14, fontWeight: 600 })
  const group = new Group([background, label], { left: center.x - dimensions.width / 2, top: center.y - dimensions.height / 2, selectable: false, evented: false, opacity: .86 }) as StudioObject
  group.data = { kind: 'generating', prompt: requestedPrompt }
  addToCanvas(group, false)
  return group
}

function replacePlaceholder(placeholder: StudioObject, replacement?: StudioObject) {
  if (!canvas) return
  canvas.remove(placeholder)
  if (replacement) canvas.setActiveObject(replacement)
  canvas.requestRenderAll()
  syncCanvasState()
}

function zoomAt(point: Point, nextZoom: number) {
  if (!canvas) return
  const value = Math.min(3, Math.max(.15, nextZoom))
  canvas.zoomToPoint(point, value)
  canvas.requestRenderAll()
  syncCanvasState()
}

function zoomBy(factor: number) {
  if (!canvas) return
  zoomAt(new Point(canvas.getWidth() / 2, canvas.getHeight() / 2), canvas.getZoom() * factor)
}

function fitCanvas() {
  if (!canvas) return
  const objects = canvas.getObjects()
  if (!objects.length) {
    canvas.setViewportTransform([1, 0, 0, 1, canvas.getWidth() / 2, canvas.getHeight() / 2])
    syncCanvasState()
    return
  }
  const bounds = objects.map((object) => object.getBoundingRect())
  const minX = Math.min(...bounds.map((item) => item.left))
  const minY = Math.min(...bounds.map((item) => item.top))
  const maxX = Math.max(...bounds.map((item) => item.left + item.width))
  const maxY = Math.max(...bounds.map((item) => item.top + item.height))
  const padding = Math.min(150, canvas.getWidth() * .14)
  const scale = Math.min((canvas.getWidth() - padding * 2) / Math.max(maxX - minX, 1), (canvas.getHeight() - padding * 2) / Math.max(maxY - minY, 1), 1.35)
  canvas.setViewportTransform([scale, 0, 0, scale, (canvas.getWidth() - (maxX - minX) * scale) / 2 - minX * scale, (canvas.getHeight() - (maxY - minY) * scale) / 2 - minY * scale])
  canvas.requestRenderAll()
  syncCanvasState()
}

function removeSelection() {
  if (!canvas) return
  const active = canvas.getActiveObjects() as StudioObject[]
  active.forEach((object) => {
    if (object.data?.objectURL) {
      URL.revokeObjectURL(object.data.objectURL)
      objectURLs.delete(object.data.objectURL)
    }
    canvas?.remove(object)
  })
  canvas.discardActiveObject()
  canvas.requestRenderAll()
  syncCanvasState()
}

async function duplicateSelection() {
  if (!canvas) return
  const active = canvas.getActiveObject() as StudioObject | undefined
  if (!active) return
  const clone = await active.clone() as StudioObject
  clone.set({ left: (active.left || 0) + 32, top: (active.top || 0) + 32 })
  clone.data = { ...active.data }
  addToCanvas(clone)
}

function downloadSelection() {
  if (!canvas) return
  const active = canvas.getActiveObject()
  if (!active) return
  const dataURL = active.toDataURL({ format: 'png', multiplier: 2 })
  const link = document.createElement('a')
  link.href = dataURL
  link.download = `dengdeng-${Date.now()}.png`
  document.body.appendChild(link)
  link.click()
  link.remove()
}

async function importFiles(files: FileList | File[], dropPoint?: Point) {
  const images = Array.from(files).filter((file) => file.type.startsWith('image/'))
  for (const [index, file] of images.entries()) {
    const src = URL.createObjectURL(file)
    objectURLs.add(src)
    const point = dropPoint || canvasCenter()
    await addImage(src, { kind: 'image', prompt: file.name, objectURL: src }, new Point(point.x + index * 28, point.y + index * 28))
  }
  if (fileInput.value) fileInput.value.value = ''
}

function onDrop(event: DragEvent) {
  if (event.dataTransfer?.files.length) importFiles(event.dataTransfer.files, scenePoint(event.clientX, event.clientY))
}

async function generate() {
  if (!canGenerate.value || !canvas) return
  generating.value = true
  error.value = ''
  const requestedPrompt = prompt.value.trim()
  const placeholder = addGeneratingPlaceholder(requestedPrompt)
  try {
    const response = await fetch('/v1/images/generations', {
      method: 'POST',
      headers: { Authorization: `Bearer ${apiKey.value.trim()}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: model.value, prompt: requestedPrompt, size: size.value, quality: quality.value, background: 'auto', output_format: 'png', n: 1 }),
    })
    const payload = await response.json().catch(() => null) as { data?: ImageResponseItem[] } | null
    if (!response.ok) throw new Error(localizedApiError(response.status, payload))
    const incoming = Array.isArray(payload?.data) ? payload.data.filter((item) => imageSource(item)) : []
    if (!incoming.length) throw new Error('接口没有返回可展示的图像')
    const point = new Point((placeholder.left || 0) + placeholder.getScaledWidth() / 2, (placeholder.top || 0) + placeholder.getScaledHeight() / 2)
    const image = await addImage(imageSource(incoming[0]), { kind: 'image', prompt: requestedPrompt, revisedPrompt: incoming[0].revised_prompt || '' }, point)
    replacePlaceholder(placeholder, image || undefined)
    for (const [index, item] of incoming.slice(1).entries()) {
      await addImage(imageSource(item), { kind: 'image', prompt: requestedPrompt, revisedPrompt: item.revised_prompt || '' }, new Point(point.x + (index + 1) * 36, point.y + (index + 1) * 36))
    }
  } catch (cause) {
    const message = cause instanceof Error ? cause.message : '生成失败，请稍后再试'
    error.value = message.includes('401') || /unauthorized|invalid api key|密钥/i.test(message) ? '密钥无效或已失效，请重新粘贴后再试。' : localizeErrorMessage(message)
    replacePlaceholder(placeholder)
  } finally {
    generating.value = false
  }
}

function clearCanvas() {
  if (!canvas || !canvas.getObjects().length || !window.confirm('清空当前画布？')) return
  canvas.clear()
  canvas.backgroundColor = 'transparent'
  objectURLs.forEach((url) => URL.revokeObjectURL(url))
  objectURLs.clear()
  syncCanvasState()
}

function setTool(nextTool: StudioTool) {
  tool.value = nextTool
  if (!canvas) return
  canvas.selection = nextTool === 'select'
  canvas.defaultCursor = nextTool === 'hand' ? 'grab' : nextTool === 'note' ? 'crosshair' : 'default'
  canvas.hoverCursor = nextTool === 'hand' ? 'grab' : 'move'
  canvas.requestRenderAll()
}

function onKeyDown(event: KeyboardEvent) {
  const target = event.target as HTMLElement | null
  const editing = target?.matches('input, textarea, [contenteditable="true"]')
  if (event.code === 'Space' && !editing) { spacePressed.value = true; event.preventDefault() }
  if (editing) return
  if (event.key === 'Delete' || event.key === 'Backspace') removeSelection()
  if ((event.metaKey || event.ctrlKey) && event.key === '0') { event.preventDefault(); fitCanvas() }
  if (event.key === '+' || event.key === '=') zoomBy(1.15)
  if (event.key === '-') zoomBy(.87)
  if (event.key.toLowerCase() === 'v') setTool('select')
  if (event.key.toLowerCase() === 'h') setTool('hand')
  if (event.key.toLowerCase() === 'n') setTool('note')
}

function onKeyUp(event: KeyboardEvent) {
  if (event.code === 'Space') spacePressed.value = false
}

function initializeCanvas() {
  if (!canvasElement.value || !viewportElement.value) return
  canvas = new Canvas(canvasElement.value, {
    preserveObjectStacking: true,
    selectionColor: 'rgba(207,141,38,.08)',
    selectionBorderColor: '#cf8d26',
    selectionLineWidth: 1.5,
    fireRightClick: true,
    stopContextMenu: true,
  })
  const resize = () => {
    if (!canvas || !viewportElement.value) return
    canvas.setDimensions({ width: viewportElement.value.clientWidth, height: viewportElement.value.clientHeight })
    canvas.requestRenderAll()
  }
  resizeObserver = new ResizeObserver(resize)
  resizeObserver.observe(viewportElement.value)
  resize()
  fitCanvas()

  canvas.on('selection:created', syncCanvasState)
  canvas.on('selection:updated', syncCanvasState)
  canvas.on('selection:cleared', syncCanvasState)
  canvas.on('object:added', syncCanvasState)
  canvas.on('object:removed', syncCanvasState)
  canvas.on('mouse:wheel', ({ e }) => {
    e.preventDefault()
    e.stopPropagation()
    if (e.ctrlKey || e.metaKey) zoomAt(new Point(e.offsetX, e.offsetY), canvas!.getZoom() * Math.exp(-e.deltaY * .002))
    else {
      const transform = canvas!.viewportTransform
      transform[4] -= e.deltaX
      transform[5] -= e.deltaY
      canvas!.setViewportTransform(transform)
      syncCanvasState()
    }
  })
  canvas.on('mouse:down', ({ e, target, scenePoint: point }) => {
    if (tool.value === 'note' && !target) { addNoteAt(point); return }
    if (!target || tool.value === 'hand' || spacePressed.value) {
      isPanning = true
      lastPointer = { x: e.clientX, y: e.clientY }
      canvas!.selection = false
      canvas!.defaultCursor = 'grabbing'
    }
  })
  canvas.on('mouse:move', ({ e }) => {
    if (!isPanning || !canvas) return
    const transform = canvas.viewportTransform
    transform[4] += e.clientX - lastPointer.x
    transform[5] += e.clientY - lastPointer.y
    lastPointer = { x: e.clientX, y: e.clientY }
    canvas.setViewportTransform(transform)
  })
  canvas.on('mouse:up', () => {
    if (!canvas) return
    isPanning = false
    canvas.selection = tool.value === 'select'
    canvas.defaultCursor = tool.value === 'hand' ? 'grab' : tool.value === 'note' ? 'crosshair' : 'default'
    canvas.setViewportTransform(canvas.viewportTransform)
    syncCanvasState()
  })
}

onMounted(async () => {
  apiKey.value = readSessionKey()
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('keyup', onKeyUp)
  await nextTick()
  initializeCanvas()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('keyup', onKeyUp)
  resizeObserver?.disconnect()
  canvas?.dispose()
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
      <div class="studio-document-title"><strong>未命名画布</strong><span>{{ objectCount }} 个对象 · Fabric.js</span></div>
      <div class="studio-topbar-actions"><button type="button" :disabled="!objectCount" @click="clearCanvas">清空</button><RouterLink to="/login">控制台</RouterLink></div>
    </header>

    <main class="studio-canvas-shell">
      <aside class="studio-tool-rail" aria-label="画布工具">
        <button type="button" :class="{ 'is-active': tool === 'select' }" aria-label="选择工具" title="选择 V" @click="setTool('select')"><svg viewBox="0 0 24 24"><path d="m5 3 13.5 8.2-6.2 1.2-3.1 5.4L5 3Z" /></svg></button>
        <button type="button" :class="{ 'is-active': tool === 'hand' }" aria-label="抓手工具" title="抓手 H" @click="setTool('hand')"><svg viewBox="0 0 24 24"><path d="M7.5 11V7.2a1.5 1.5 0 0 1 3 0V10m0-3.8a1.5 1.5 0 0 1 3 0V10m0-2.8a1.5 1.5 0 0 1 3 0v4m0-2a1.5 1.5 0 0 1 3 0v4.2c0 4.2-2.5 6.6-6.5 6.6h-1.1a6 6 0 0 1-4.3-1.8L4.8 15a1.7 1.7 0 0 1 2.4-2.4l1.3 1.1" /></svg></button>
        <button type="button" :class="{ 'is-active': tool === 'note' }" aria-label="添加便签" title="便签 N" @click="setTool('note')"><svg viewBox="0 0 24 24"><path d="M5 4h14v12l-4 4H5V4Z" /><path d="M15 20v-4h4M8 8h8M8 12h5" /></svg></button>
        <button type="button" aria-label="导入图片" title="导入图片" @click="fileInput?.click()"><svg viewBox="0 0 24 24"><path d="M4 5h16v14H4z" /><circle cx="9" cy="10" r="1.5" /><path d="m5 17 4.5-4 3 2.5 2.5-2 4 3.5" /></svg></button>
        <span class="studio-tool-divider"></span>
        <button type="button" aria-label="适配画布" title="适配画布 ⌘0" @click="fitCanvas"><svg viewBox="0 0 24 24"><path d="M8 3H3v5M16 3h5v5M8 21H3v-5M16 21h5v-5" /></svg></button>
        <input ref="fileInput" class="sr-only" type="file" accept="image/*" multiple @change="($event.target as HTMLInputElement).files && importFiles(($event.target as HTMLInputElement).files!)" />
      </aside>

      <section ref="viewportElement" class="studio-infinite-canvas studio-fabric-canvas" aria-label="无限画布" @dragover.prevent @drop.prevent="onDrop">
        <canvas ref="canvasElement"></canvas>
        <div v-if="!objectCount" class="studio-canvas-empty" aria-hidden="true"><span>拖入图片或从右侧生成 · ⌘/Ctrl + 滚轮缩放</span></div>
        <div v-if="hasSelection" class="studio-selection-actions">
          <button type="button" @click="downloadSelection">导出</button>
          <button type="button" @click="duplicateSelection">复制</button>
          <button type="button" class="is-danger" @click="removeSelection">删除</button>
        </div>
        <div class="studio-zoom-control"><button type="button" aria-label="缩小" @click="zoomBy(.85)">−</button><button type="button" class="studio-zoom-value" aria-label="适配画布" @click="fitCanvas">{{ zoomLabel }}</button><button type="button" aria-label="放大" @click="zoomBy(1.15)">＋</button></div>
      </section>

      <aside class="studio-generator-panel" aria-label="生成设置">
        <div class="studio-generator-head"><div><strong>生成</strong><span>⌘ Enter</span></div><span class="studio-key-status" :class="{ 'is-ready': hasKey }">{{ hasKey ? '密钥已就绪' : '需要密钥' }}</span></div>
        <label class="studio-generator-field studio-generator-key"><span>API 密钥</span><div><input v-model="apiKey" :type="revealKey ? 'text' : 'password'" autocomplete="off" autocapitalize="none" spellcheck="false" placeholder="dd-…" /><button type="button" :aria-label="revealKey ? '隐藏密钥' : '显示密钥'" @click="revealKey = !revealKey"><svg v-if="revealKey" viewBox="0 0 24 24"><path d="m3 3 18 18M10.6 10.7a2 2 0 0 0 2.7 2.7M6.2 6.2C4.2 7.6 3 9.8 3 12c0 2.6 3.7 7 9 7 1.2 0 2.3-.2 3.3-.6M9.9 5.2A10.6 10.6 0 0 1 12 5c5.3 0 9 4.4 9 7 0 1.8-1.3 3.6-3.1 5" /></svg><svg v-else viewBox="0 0 24 24"><path d="M3 12s3.2-7 9-7 9 7 9 7-3.2 7-9 7-9-7-9-7Z" /><circle cx="12" cy="12" r="2.5" /></svg></button><button v-if="apiKey" type="button" aria-label="清除密钥" @click="clearKey">×</button></div></label>
        <label class="studio-generator-field studio-prompt-field"><span>画面描述</span><textarea v-model="prompt" rows="7" placeholder="描述你想生成的画面" @keydown.meta.enter.prevent="generate" @keydown.ctrl.enter.prevent="generate"></textarea></label>
        <label class="studio-generator-field"><span>模型</span><input v-model="model" class="studio-model-input" spellcheck="false" /></label>
        <div class="studio-generator-field"><span>画幅</span><div class="studio-segmented studio-segmented--shape"><button v-for="option in sizeOptions" :key="option.id" type="button" :class="{ 'is-active': size === option.id }" :title="option.id" @click="size = option.id"><i :class="`is-${option.shape}`"></i>{{ option.label }}</button></div></div>
        <div class="studio-generator-field"><span>质量</span><div class="studio-segmented"><button v-for="option in qualityOptions" :key="option.id" type="button" :class="{ 'is-active': quality === option.id }" @click="quality = option.id">{{ option.label }}</button></div></div>
        <p v-if="error" class="studio-generator-error" role="alert">{{ error }}</p>
        <button type="button" class="studio-generate-button" :disabled="!canGenerate" @click="generate"><span v-if="generating" class="studio-button-loader" aria-hidden="true"></span>{{ generating ? '正在生成' : '生成到画布' }}</button>
      </aside>
    </main>
  </div>
</template>
