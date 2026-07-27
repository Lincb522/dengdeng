import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

export type ColorMode = 'light' | 'dark' | 'system'
export type ResolvedColorMode = Exclude<ColorMode, 'system'>

const legacyModeStorageKey = 'dengdeng.theme'
const modeStorageKey = 'dengdeng.color-mode'
const retiredPresetStorageKey = 'dengdeng.ui-theme'

function isColorMode(value: string | null): value is ColorMode {
  return value === 'light' || value === 'dark' || value === 'system'
}

function readStorage(key: string) {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeStorage(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // The active page still receives the display mode when storage is unavailable.
  }
}

function removeStorage(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
}

export const useTheme = defineStore('theme', () => {
  const colorMode = ref<ColorMode>('system')
  const resolvedMode = ref<ResolvedColorMode>('light')
  const isDark = computed(() => resolvedMode.value === 'dark')
  let mediaQuery: MediaQueryList | null = null

  function resolveMode(next: ColorMode): ResolvedColorMode {
    if (next !== 'system') return next
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }

  function apply() {
    const nextMode = resolveMode(colorMode.value)
    resolvedMode.value = nextMode
    document.documentElement.dataset.theme = nextMode
    delete document.documentElement.dataset.uiTheme
    delete document.documentElement.dataset.layout
    delete document.documentElement.dataset.density
    document.documentElement.style.colorScheme = nextMode
    document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
      ?.setAttribute('content', nextMode === 'dark' ? '#181613' : '#fffaf1')
  }

  function setColorMode(next: ColorMode) {
    colorMode.value = next
    writeStorage(modeStorageKey, next)
    writeStorage(legacyModeStorageKey, next === 'system' ? resolveMode(next) : next)
    apply()
  }

  function handleSystemModeChange() {
    if (colorMode.value === 'system') apply()
  }

  function init() {
    const savedMode = readStorage(modeStorageKey)
    const legacyMode = readStorage(legacyModeStorageKey)
    colorMode.value = isColorMode(savedMode)
      ? savedMode
      : isColorMode(legacyMode) && legacyMode !== 'system' ? legacyMode : 'system'

    removeStorage(retiredPresetStorageKey)
    mediaQuery?.removeEventListener('change', handleSystemModeChange)
    mediaQuery = window.matchMedia?.('(prefers-color-scheme: dark)') ?? null
    mediaQuery?.addEventListener('change', handleSystemModeChange)
    apply()
  }

  function toggle() {
    setColorMode(isDark.value ? 'light' : 'dark')
  }

  return {
    colorMode,
    resolvedMode,
    mode: resolvedMode,
    isDark,
    init,
    toggle,
    setColorMode,
  }
})
