import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface Toast {
  id: number
  message: string
  kind: 'success' | 'error' | 'info'
}

let nextId = 1

export const useToast = defineStore('toast', () => {
  const toasts = ref<Toast[]>([])

  function show(message: string, kind: Toast['kind'] = 'info') {
    const id = nextId++
    toasts.value.push({ id, message, kind })
    setTimeout(() => {
      toasts.value = toasts.value.filter((t) => t.id !== id)
    }, 3200)
  }

  return { toasts, show }
})
