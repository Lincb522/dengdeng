<script lang="ts">
let modalSequence = 0
</script>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

type ModalWidth = 'simple' | 'standard' | 'wide' | 'setup'

const props = withDefaults(defineProps<{
  open: boolean
  title: string
  description?: string
  width?: ModalWidth
  busy?: boolean
  closeOnBackdrop?: boolean
  initialFocus?: string
}>(), {
  description: '',
  width: 'standard',
  busy: false,
  closeOnBackdrop: true,
  initialFocus: '',
})

const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement | null>(null)
const backdropPointerStarted = ref(false)
let returnFocus: HTMLElement | null = null
let locked = false
const instanceID = ++modalSequence
const titleID = `app-modal-title-${instanceID}`
const descriptionID = `app-modal-description-${instanceID}`

const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function lockPage() {
  if (locked) return
  locked = true
  document.body.dataset.modalOpenCount = String(Number(document.body.dataset.modalOpenCount || 0) + 1)
  document.body.classList.add('has-app-modal')
}

function unlockPage() {
  if (!locked) return
  locked = false
  const nextCount = Math.max(0, Number(document.body.dataset.modalOpenCount || 1) - 1)
  document.body.dataset.modalOpenCount = String(nextCount)
  if (!nextCount) document.body.classList.remove('has-app-modal')
}

function requestClose() {
  if (props.busy) return
  backdropPointerStarted.value = false
  emit('close')
}

function handleBackdropPointerDown(event: PointerEvent) {
  backdropPointerStarted.value = event.target === event.currentTarget
}

function handleBackdropPointerUp(event: PointerEvent) {
  const shouldClose = props.closeOnBackdrop && backdropPointerStarted.value && event.target === event.currentTarget
  backdropPointerStarted.value = false
  if (shouldClose) requestClose()
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    requestClose()
    return
  }
  if (event.key !== 'Tab' || !panel.value) return
  const focusable = [...panel.value.querySelectorAll<HTMLElement>(focusableSelector)]
    .filter((element) => !element.hasAttribute('hidden') && element.offsetParent !== null)
  if (!focusable.length) {
    event.preventDefault()
    panel.value.focus()
    return
  }
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(() => props.open, async (open) => {
  if (open) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    lockPage()
    await nextTick()
    const preferred = props.initialFocus ? panel.value?.querySelector<HTMLElement>(props.initialFocus) : null
    const first = panel.value?.querySelector<HTMLElement>('.app-modal__body [autofocus], .app-modal__body input:not([disabled]), .app-modal__body select:not([disabled]), .app-modal__body textarea:not([disabled]), .app-modal__body button:not([disabled])')
    ;(preferred || first || panel.value)?.focus()
    return
  }
  unlockPage()
  await nextTick()
  if (returnFocus?.isConnected) returnFocus.focus()
  returnFocus = null
}, { immediate: true })

onBeforeUnmount(unlockPage)
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="app-modal-backdrop"
      @keydown="handleKeydown"
      @pointercancel="backdropPointerStarted = false"
      @pointerdown="handleBackdropPointerDown"
      @pointerup="handleBackdropPointerUp"
    >
      <section
        ref="panel"
        class="app-modal"
        :class="`app-modal--${width}`"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="titleID"
        :aria-describedby="description ? descriptionID : undefined"
        tabindex="-1"
      >
        <header class="app-modal__header">
          <div class="app-modal__heading">
            <h2 :id="titleID">{{ title }}</h2>
            <p v-if="description" :id="descriptionID">{{ description }}</p>
            <slot name="header-meta" />
          </div>
          <button type="button" class="app-modal__close" :disabled="busy" aria-label="关闭弹窗" @click="requestClose">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg>
          </button>
        </header>
        <div class="app-modal__body">
          <slot />
        </div>
        <footer v-if="$slots.footer" class="app-modal__footer">
          <slot name="footer" />
        </footer>
      </section>
    </div>
  </Teleport>
</template>
