import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

export type ThemeMode = 'light' | 'dark'

const storageKey = 'dengdeng.theme'

export const useTheme = defineStore('theme', () => {
  const mode = ref<ThemeMode>('light')
  const isDark = computed(() => mode.value === 'dark')

  function apply(next: ThemeMode) {
    mode.value = next
    document.documentElement.dataset.theme = next
    document.documentElement.style.colorScheme = next
  }

  function init() {
    const saved = localStorage.getItem(storageKey)
    const next: ThemeMode = saved === 'dark' || saved === 'light'
      ? saved
      : window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
    apply(next)
  }

  function toggle() {
    const next: ThemeMode = mode.value === 'dark' ? 'light' : 'dark'
    localStorage.setItem(storageKey, next)
    apply(next)
  }

  return { mode, isDark, init, toggle }
})
