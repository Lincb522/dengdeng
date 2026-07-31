<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Canvas, FabricImage, Group, Point, Rect, Text, Textbox, util, type FabricObject } from 'fabric'
import { api, getToken } from '../api/client'
import { localizedApiError, localizeErrorMessage } from '../api/errors'
import type { ApiKey, ModelCatalogueItem } from '../api/types'

interface ImageResponseItem {
  b64_json?: string
  url?: string
  revised_prompt?: string
}

type StudioTool = 'select' | 'hand' | 'note'
type StudioObject = FabricObject & { data?: { kind?: string; prompt?: string; revisedPrompt?: string; objectURL?: string } }

interface StudioAsset {
  id: string
  src: string
  name: string
  prompt: string
  createdAt: string
  source: 'generated' | 'imported'
}

interface StudioSnapshot {
  id: string
  label: string
  createdAt: string
  json: Record<string, unknown>
}

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
const settingsOpen = ref(false)
const optionsLoading = ref(false)
const keys = ref<ApiKey[]>([])
const imageModels = ref<ModelCatalogueItem[]>([])
const selectedKeyID = ref(0)
const keySecrets = new Map<number, string>()
const draftOpen = ref(false)
const draftPoint = ref(new Point(0, 0))
const draftPosition = ref({ x: 0, y: 0 })
const assetPanelOpen = ref(false)
const historyPanelOpen = ref(false)
const assets = ref<StudioAsset[]>([])
const snapshots = ref<StudioSnapshot[]>([])
const snapshotIndex = ref(-1)
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
let restoringSnapshot = false
let snapshotTimer = 0
let studioDB: Promise<IDBDatabase> | null = null

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
const canGenerate = computed(() => (usingAccountKey.value || hasKey.value) && Boolean(prompt.value.trim()) && !generating.value)
const zoomLabel = computed(() => `${Math.round(zoom.value * 100)}%`)
const activeKeys = computed(() => keys.value.filter((key) => key.status === 'active' && key.secret_available))
const selectedKey = computed(() => activeKeys.value.find((key) => key.id === selectedKeyID.value) || null)
const usingAccountKey = computed(() => Boolean(selectedKey.value))
const canUndo = computed(() => snapshotIndex.value > 0)
const canRedo = computed(() => snapshotIndex.value >= 0 && snapshotIndex.value < snapshots.value.length - 1)

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

function openStudioDB() {
  if (studioDB) return studioDB
  studioDB = new Promise((resolve, reject) => {
    const request = indexedDB.open('dengdeng-image-workbench', 1)
    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains('workspace')) db.createObjectStore('workspace')
      if (!db.objectStoreNames.contains('assets')) db.createObjectStore('assets', { keyPath: 'id' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
  return studioDB
}

async function dbGet<T>(store: string, key: IDBValidKey) {
  const db = await openStudioDB()
  return new Promise<T | undefined>((resolve, reject) => {
    const request = db.transaction(store, 'readonly').objectStore(store).get(key)
    request.onsuccess = () => resolve(request.result as T | undefined)
    request.onerror = () => reject(request.error)
  })
}

async function dbPut(store: string, value: unknown, key?: IDBValidKey) {
  const db = await openStudioDB()
  return new Promise<void>((resolve, reject) => {
    const transaction = db.transaction(store, 'readwrite')
    const objectStore = transaction.objectStore(store)
    if (key === undefined) objectStore.put(value)
    else objectStore.put(value, key)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error)
  })
}

async function dbDelete(store: string, key: IDBValidKey) {
  const db = await openStudioDB()
  return new Promise<void>((resolve, reject) => {
    const transaction = db.transaction(store, 'readwrite')
    transaction.objectStore(store).delete(key)
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error)
  })
}

async function dbAll<T>(store: string) {
  const db = await openStudioDB()
  return new Promise<T[]>((resolve, reject) => {
    const request = db.transaction(store, 'readonly').objectStore(store).getAll()
    request.onsuccess = () => resolve((request.result || []) as T[])
    request.onerror = () => reject(request.error)
  })
}

function fileDataURL(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

async function saveAsset(asset: StudioAsset) {
  assets.value = [asset, ...assets.value.filter((item) => item.id !== asset.id)].slice(0, 120)
  try { await dbPut('assets', asset) } catch { /* Canvas remains usable if storage is unavailable. */ }
}

async function removeAsset(asset: StudioAsset) {
  assets.value = assets.value.filter((item) => item.id !== asset.id)
  try { await dbDelete('assets', asset.id) } catch { /* Ignore storage cleanup failures. */ }
}

async function placeAsset(asset: StudioAsset) {
  await addImage(asset.src, { kind: 'image', prompt: asset.prompt || asset.name }, canvasCenter())
  assetPanelOpen.value = false
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

function canvasSnapshotJSON() {
  if (!canvas) return {}
  return {
    canvas: canvas.toObject(['data']),
    viewportTransform: [...canvas.viewportTransform],
  }
}

async function persistSnapshots() {
  try {
    await dbPut('workspace', { snapshots: snapshots.value, index: snapshotIndex.value }, 'history')
  } catch { /* History remains available in memory. */ }
}

function captureSnapshot(label: string) {
  if (!canvas || restoringSnapshot || canvas.getObjects().some((object) => (object as StudioObject).data?.kind === 'generating')) return
  const json = canvasSnapshotJSON()
  const serialized = JSON.stringify(json)
  const current = snapshots.value[snapshotIndex.value]
  if (current && JSON.stringify(current.json) === serialized) return
  const next = snapshots.value.slice(0, snapshotIndex.value + 1)
  next.push({ id: `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`, label, createdAt: new Date().toISOString(), json })
  // Data URLs can make a single canvas snapshot several megabytes. Keep a
  // deeper history for light canvases and a bounded one for image-heavy work.
  const limit = serialized.length > 5_000_000 ? 6 : serialized.length > 1_000_000 ? 12 : 40
  snapshots.value = next.slice(-limit)
  snapshotIndex.value = snapshots.value.length - 1
  void persistSnapshots()
}

function scheduleSnapshot(label: string) {
  window.clearTimeout(snapshotTimer)
  snapshotTimer = window.setTimeout(() => captureSnapshot(label), 260)
}

async function restoreSnapshot(index: number) {
  if (!canvas || index < 0 || index >= snapshots.value.length) return
  restoringSnapshot = true
  try {
    const snapshot = snapshots.value[index]
    const payload = snapshot.json as { canvas?: Record<string, unknown>; viewportTransform?: number[] }
    await canvas.loadFromJSON(payload.canvas || {})
    if (Array.isArray(payload.viewportTransform) && payload.viewportTransform.length === 6) canvas.setViewportTransform(payload.viewportTransform)
    canvas.getObjects().forEach((object) => configureObject(object as StudioObject))
    canvas.discardActiveObject()
    canvas.requestRenderAll()
    snapshotIndex.value = index
    syncCanvasState()
    await persistSnapshots()
  } finally {
    restoringSnapshot = false
  }
}

function undo() {
  if (canUndo.value) void restoreSnapshot(snapshotIndex.value - 1)
}

function redo() {
  if (canRedo.value) void restoreSnapshot(snapshotIndex.value + 1)
}

function historyTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

async function loadSavedWorkspace() {
  try {
    assets.value = (await dbAll<StudioAsset>('assets')).sort((a, b) => b.createdAt.localeCompare(a.createdAt))
    const saved = await dbGet<{ snapshots?: StudioSnapshot[]; index?: number }>('workspace', 'history')
    snapshots.value = Array.isArray(saved?.snapshots) ? saved.snapshots.slice(-40) : []
    snapshotIndex.value = Math.min(Number(saved?.index ?? snapshots.value.length - 1), snapshots.value.length - 1)
    if (snapshotIndex.value >= 0) await restoreSnapshot(snapshotIndex.value)
    else captureSnapshot('新建画布')
  } catch {
    if (!snapshots.value.length) captureSnapshot('新建画布')
  }
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

function addGeneratingPlaceholder(requestedPrompt: string, point = canvasCenter()) {
  const center = point
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
    const src = await fileDataURL(file)
    const point = dropPoint || canvasCenter()
    await addImage(src, { kind: 'image', prompt: file.name }, new Point(point.x + index * 28, point.y + index * 28))
    await saveAsset({ id: `${Date.now()}-${index}`, src, name: file.name, prompt: '', createdAt: new Date().toISOString(), source: 'imported' })
  }
  scheduleSnapshot('导入图片')
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
  const generationPoint = draftOpen.value ? draftPoint.value : canvasCenter()
  const placeholder = addGeneratingPlaceholder(requestedPrompt, generationPoint)
  try {
    const generationKey = await resolveAPIKey()
    const response = await fetch('/v1/images/generations', {
      method: 'POST',
      headers: { Authorization: `Bearer ${generationKey}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: model.value, prompt: requestedPrompt, size: size.value, quality: quality.value, background: 'auto', output_format: 'png', n: 1 }),
    })
    const payload = await response.json().catch(() => null) as { data?: ImageResponseItem[] } | null
    if (!response.ok) throw new Error(localizedApiError(response.status, payload))
    const incoming = Array.isArray(payload?.data) ? payload.data.filter((item) => imageSource(item)) : []
    if (!incoming.length) throw new Error('接口没有返回可展示的图像')
    const point = new Point((placeholder.left || 0) + placeholder.getScaledWidth() / 2, (placeholder.top || 0) + placeholder.getScaledHeight() / 2)
    const image = await addImage(imageSource(incoming[0]), { kind: 'image', prompt: requestedPrompt, revisedPrompt: incoming[0].revised_prompt || '' }, point)
    await saveAsset({ id: `${Date.now()}-generated`, src: imageSource(incoming[0]), name: requestedPrompt.slice(0, 36) || '生成图片', prompt: requestedPrompt, createdAt: new Date().toISOString(), source: 'generated' })
    replacePlaceholder(placeholder, image || undefined)
    for (const [index, item] of incoming.slice(1).entries()) {
      await addImage(imageSource(item), { kind: 'image', prompt: requestedPrompt, revisedPrompt: item.revised_prompt || '' }, new Point(point.x + (index + 1) * 36, point.y + (index + 1) * 36))
      await saveAsset({ id: `${Date.now()}-generated-${index}`, src: imageSource(item), name: requestedPrompt.slice(0, 36) || '生成图片', prompt: requestedPrompt, createdAt: new Date().toISOString(), source: 'generated' })
    }
    scheduleSnapshot('生成图片')
    draftOpen.value = false
    prompt.value = ''
  } catch (cause) {
    const message = cause instanceof Error ? cause.message : '生成失败，请稍后再试'
    error.value = message.includes('401') || /unauthorized|invalid api key|密钥/i.test(message) ? '密钥无效或已失效，请重新粘贴后再试。' : localizeErrorMessage(message)
    replacePlaceholder(placeholder)
  } finally {
    generating.value = false
  }
}

async function resolveAPIKey() {
  if (!selectedKeyID.value) return apiKey.value.trim()
  const cached = keySecrets.get(selectedKeyID.value)
  if (cached) return cached
  const result = await api.get<{ plain: string }>(`/api/user/keys/${selectedKeyID.value}/secret`)
  if (!result?.plain) throw new Error('无法读取所选密钥，请在密钥管理中确认该密钥可用')
  keySecrets.set(selectedKeyID.value, result.plain)
  return result.plain
}

async function loadWorkbenchOptions() {
  optionsLoading.value = true
  try {
    const catalogue = await api.get<ModelCatalogueItem[] | null>('/api/models')
    imageModels.value = (Array.isArray(catalogue) ? catalogue : []).filter((item) => item.kind === 'image' && item.available)
    if (imageModels.value.length && !imageModels.value.some((item) => item.name === model.value)) model.value = imageModels.value[0].name
    if (getToken()) {
      keys.value = await api.get<ApiKey[]>('/api/user/keys')
      const current = activeKeys.value.find((key) => key.id === selectedKeyID.value) || activeKeys.value[0]
      selectedKeyID.value = current?.id || 0
    }
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '工作台设置加载失败'
  } finally {
    optionsLoading.value = false
  }
}

function openGenerator(point: Point, clientX: number, clientY: number) {
  if (!viewportElement.value) return
  const rect = viewportElement.value.getBoundingClientRect()
  draftPoint.value = point
  draftPosition.value = {
    x: Math.min(Math.max(clientX - rect.left, 160), rect.width - 160),
    y: Math.min(Math.max(clientY - rect.top, 110), rect.height - 150),
  }
  draftOpen.value = true
  window.setTimeout(() => document.querySelector<HTMLTextAreaElement>('.studio-node-composer textarea')?.focus(), 0)
}

function clearCanvas() {
  if (!canvas || !canvas.getObjects().length || !window.confirm('清空当前画布？')) return
  canvas.clear()
  canvas.backgroundColor = 'transparent'
  objectURLs.forEach((url) => URL.revokeObjectURL(url))
  objectURLs.clear()
  syncCanvasState()
  captureSnapshot('清空画布')
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
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'z') {
    event.preventDefault()
    if (event.shiftKey) redo()
    else undo()
  }
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
  canvas.on('object:modified', () => scheduleSnapshot('调整对象'))
  canvas.on('text:changed', () => scheduleSnapshot('编辑文字'))
  canvas.on('object:added', () => scheduleSnapshot('添加对象'))
  canvas.on('object:removed', () => scheduleSnapshot('删除对象'))
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
  canvas.on('mouse:dblclick', ({ e, target, scenePoint: point }) => {
    if (!target) openGenerator(point, e.clientX, e.clientY)
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
  await loadSavedWorkspace()
  await loadWorkbenchOptions()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('keyup', onKeyUp)
  resizeObserver?.disconnect()
  window.clearTimeout(snapshotTimer)
  canvas?.dispose()
  objectURLs.forEach((url) => URL.revokeObjectURL(url))
})

watch(apiKey, persistSessionKey)
</script>

<template>
  <div class="image-studio image-studio--canvas">
    <main class="studio-canvas-shell">
      <section ref="viewportElement" class="studio-infinite-canvas studio-fabric-canvas" aria-label="无限画布" @dragover.prevent @drop.prevent="onDrop">
        <canvas ref="canvasElement"></canvas>

        <div class="studio-canvas-titlebar">
          <RouterLink to="/" aria-label="返回首页" title="返回首页"><svg viewBox="0 0 24 24"><path d="m15 5-7 7 7 7" /></svg></RouterLink>
          <div><img src="/brand/dengdeng-avatar.png" alt="" /><span><strong>未命名画布</strong><small>{{ objectCount }} 个对象</small></span></div>
        </div>

        <nav class="studio-bottom-dock" aria-label="画布工具">
          <button type="button" :class="{ 'is-active': tool === 'select' }" aria-label="选择工具" title="选择 V" @click="setTool('select')"><svg viewBox="0 0 24 24"><path d="m5 3 13.5 8.2-6.2 1.2-3.1 5.4L5 3Z" /></svg></button>
          <button type="button" :class="{ 'is-active': tool === 'hand' }" aria-label="抓手工具" title="抓手 H" @click="setTool('hand')"><svg viewBox="0 0 24 24"><path d="M7.5 11V7.2a1.5 1.5 0 0 1 3 0V10m0-3.8a1.5 1.5 0 0 1 3 0V10m0-2.8a1.5 1.5 0 0 1 3 0v4m0-2a1.5 1.5 0 0 1 3 0v4.2c0 4.2-2.5 6.6-6.5 6.6h-1.1a6 6 0 0 1-4.3-1.8L4.8 15a1.7 1.7 0 0 1 2.4-2.4l1.3 1.1" /></svg></button>
          <button type="button" :class="{ 'is-active': tool === 'note' }" aria-label="添加便签" title="便签 N" @click="setTool('note')"><svg viewBox="0 0 24 24"><path d="M5 4h14v12l-4 4H5V4Z" /><path d="M15 20v-4h4M8 8h8M8 12h5" /></svg></button>
          <button type="button" aria-label="导入图片" title="导入图片" @click="fileInput?.click()"><svg viewBox="0 0 24 24"><path d="M4 5h16v14H4z" /><circle cx="9" cy="10" r="1.5" /><path d="m5 17 4.5-4 3 2.5 2.5-2 4 3.5" /></svg></button>
          <span class="studio-tool-divider"></span>
          <button type="button" :disabled="!canUndo" aria-label="撤销" title="撤销 ⌘Z" @click="undo"><svg viewBox="0 0 24 24"><path d="m9 7-5 5 5 5M5 12h8a6 6 0 0 1 6 6" /></svg></button>
          <button type="button" :disabled="!canRedo" aria-label="重做" title="重做 ⇧⌘Z" @click="redo"><svg viewBox="0 0 24 24"><path d="m15 7 5 5-5 5M19 12h-8a6 6 0 0 0-6 6" /></svg></button>
          <button type="button" :class="{ 'is-active': historyPanelOpen }" aria-label="历史记录" title="历史记录" @click="historyPanelOpen = !historyPanelOpen; assetPanelOpen = false"><svg viewBox="0 0 24 24"><path d="M4 12a8 8 0 1 0 2.3-5.7L4 8.6M4 4v4.6h4.6M12 8v4l3 2" /></svg></button>
          <button type="button" aria-label="适配画布" title="适配画布 ⌘0" @click="fitCanvas"><svg viewBox="0 0 24 24"><path d="M8 3H3v5M16 3h5v5M8 21H3v-5M16 21h5v-5" /></svg></button>
        </nav>
        <input ref="fileInput" class="sr-only" type="file" accept="image/*" multiple @change="($event.target as HTMLInputElement).files && importFiles(($event.target as HTMLInputElement).files!)" />
        <div v-if="!objectCount && !draftOpen" class="studio-canvas-empty" aria-hidden="true"><span>双击画布开始生成</span></div>
        <button type="button" class="studio-settings-trigger" :class="{ 'is-active': settingsOpen }" aria-label="工作台设置" title="工作台设置" @click="settingsOpen = !settingsOpen">
          <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1a1.7 1.7 0 0 0 1.9.3A1.7 1.7 0 0 0 10 3V2.8h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z" /></svg>
        </button>

        <aside v-if="settingsOpen" class="studio-workbench-settings" @pointerdown.stop @dblclick.stop>
          <header><div><span>图像制作</span><strong>工作台设置</strong></div><button type="button" aria-label="关闭设置" @click="settingsOpen = false">×</button></header>
          <div v-if="optionsLoading" class="studio-settings-loading">正在读取配置</div>
          <template v-else>
            <label class="studio-generator-field">
              <span>图片生成 Key</span>
              <select v-model.number="selectedKeyID">
                <option :value="0">手动输入密钥</option>
                <option v-for="key in activeKeys" :key="key.id" :value="key.id">{{ key.name }} · {{ key.key_preview }}</option>
              </select>
            </label>
            <label v-if="!selectedKeyID" class="studio-generator-field studio-generator-key">
              <span>手动密钥</span>
              <div><input v-model="apiKey" :type="revealKey ? 'text' : 'password'" autocomplete="off" placeholder="dd-…" /><button type="button" @click="revealKey = !revealKey"><svg viewBox="0 0 24 24"><path d="M3 12s3.2-7 9-7 9 7 9 7-3.2 7-9 7-9-7-9-7Z" /><circle cx="12" cy="12" r="2.5" /></svg></button></div>
            </label>
            <label class="studio-generator-field">
              <span>生图模型</span>
              <select v-model="model"><option v-for="item in imageModels" :key="item.id" :value="item.name">{{ item.name }}</option><option v-if="!imageModels.length" value="gpt-image-2">gpt-image-2</option></select>
            </label>
            <div class="studio-model-summary"><span>当前模型</span><strong>{{ model }}</strong><small>{{ selectedKey ? selectedKey.name : '手动密钥' }} · /v1/images/generations</small></div>
            <div class="studio-generator-field"><span>默认画幅</span><div class="studio-segmented studio-segmented--shape"><button v-for="option in sizeOptions" :key="option.id" type="button" :class="{ 'is-active': size === option.id }" @click="size = option.id"><i :class="`is-${option.shape}`"></i>{{ option.label }}</button></div></div>
            <div class="studio-generator-field"><span>生成质量</span><div class="studio-segmented"><button v-for="option in qualityOptions" :key="option.id" type="button" :class="{ 'is-active': quality === option.id }" @click="quality = option.id">{{ option.label }}</button></div></div>
          </template>
        </aside>

        <div class="studio-library-controls" @pointerdown.stop @dblclick.stop>
          <button type="button" :class="{ 'is-active': assetPanelOpen }" @click="assetPanelOpen = !assetPanelOpen; historyPanelOpen = false">
            <svg viewBox="0 0 24 24"><path d="M4 5h6l2 2h8v12H4z" /></svg><span>资产管理</span><b>{{ assets.length }}</b>
          </button>
          <button type="button" aria-label="导入图片" title="导入图片" @click="fileInput?.click()"><svg viewBox="0 0 24 24"><path d="M4 5h16v14H4z" /><path d="m6 16 4-4 3 3 2-2 3 3" /></svg></button>
        </div>

        <aside v-if="assetPanelOpen" class="studio-asset-panel" @pointerdown.stop @dblclick.stop>
          <header><div><strong>资产管理</strong><span>{{ assets.length }} 项</span></div><button type="button" @click="assetPanelOpen = false">×</button></header>
          <div v-if="assets.length" class="studio-asset-grid">
            <article v-for="asset in assets" :key="asset.id">
              <button type="button" class="studio-asset-preview" :title="asset.name" @click="placeAsset(asset)"><img :src="asset.src" alt="" /></button>
              <div><span>{{ asset.name }}</span><small>{{ asset.source === 'generated' ? '生成' : '导入' }}</small></div>
              <button type="button" class="studio-asset-delete" aria-label="删除资产" @click="removeAsset(asset)">×</button>
            </article>
          </div>
          <div v-else class="studio-panel-empty">生成或导入的图片会保存在这里</div>
        </aside>

        <aside v-if="historyPanelOpen" class="studio-history-panel" @pointerdown.stop @dblclick.stop>
          <header><div><strong>历史记录</strong><span>最多保留 40 步</span></div><button type="button" @click="historyPanelOpen = false">×</button></header>
          <button v-for="(item, index) in [...snapshots].reverse()" :key="item.id" type="button" :class="{ 'is-current': snapshots.length - 1 - index === snapshotIndex }" @click="restoreSnapshot(snapshots.length - 1 - index)">
            <span>{{ item.label }}</span><time>{{ historyTime(item.createdAt) }}</time>
          </button>
        </aside>

        <form v-if="draftOpen" class="studio-node-composer" :style="{ left: `${draftPosition.x}px`, top: `${draftPosition.y}px` }" @submit.prevent="generate" @pointerdown.stop @dblclick.stop>
          <textarea v-model="prompt" rows="3" placeholder="描述要生成的画面" @keydown.meta.enter.prevent="generate" @keydown.ctrl.enter.prevent="generate"></textarea>
          <p v-if="error" class="studio-node-composer__error" role="alert">{{ error }}</p>
          <div><span>{{ model }}</span><button type="button" @click="draftOpen = false">取消</button><button type="submit" :disabled="!canGenerate">{{ generating ? '生成中' : '生成' }}</button></div>
        </form>
        <div v-if="hasSelection" class="studio-selection-actions">
          <button type="button" @click="downloadSelection">导出</button>
          <button type="button" @click="duplicateSelection">复制</button>
          <button type="button" class="is-danger" @click="removeSelection">删除</button>
        </div>
        <div class="studio-zoom-control"><button type="button" aria-label="缩小" @click="zoomBy(.85)">−</button><button type="button" class="studio-zoom-value" aria-label="适配画布" @click="fitCanvas">{{ zoomLabel }}</button><button type="button" aria-label="放大" @click="zoomBy(1.15)">＋</button></div>
      </section>
    </main>
  </div>
</template>
