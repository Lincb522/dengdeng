import { defineStore } from 'pinia'
import { ref } from 'vue'
import { isAppError, resolveApiError } from '../api/errors'

export interface Toast {
  id: number
  message: string
  kind: 'success' | 'error' | 'info'
  title?: string
  action?: string
  requestId?: string
}

let nextId = 1

export const useToast = defineStore('toast', () => {
  const toasts = ref<Toast[]>([])

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((toast) => toast.id !== id)
  }

  function show(message: string, kind: Toast['kind'] = 'info', meta: Omit<Toast, 'id' | 'message' | 'kind'> = {}) {
    const id = nextId++
    toasts.value.push({ id, message, kind, ...meta })
    const duration = kind === 'error' ? 6500 : kind === 'success' ? 3200 : 4500
    setTimeout(() => {
      dismiss(id)
    }, duration)
    return id
  }

  function showError(error: unknown, fallback = '操作未完成，请稍后重试') {
    if (isAppError(error)) {
      return show(error.message, 'error', {
        title: error.title,
        action: error.action,
        requestId: error.status >= 500 ? error.requestId : undefined,
      })
    }
    if (error instanceof Error) {
      const resolved = resolveApiError(0, { message: error.message })
      return show(resolved.message || fallback, 'error', { title: resolved.title, action: resolved.action })
    }
    return show(fallback, 'error', { title: '操作未完成' })
  }

  return { toasts, show, showError, dismiss }
})
