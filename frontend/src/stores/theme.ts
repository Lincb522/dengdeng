import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

export type ColorMode = 'light' | 'dark' | 'system'
export type ResolvedColorMode = Exclude<ColorMode, 'system'>
export type InterfaceTheme = 'classic' | 'control' | 'pastel'

const legacyModeStorageKey = 'dengdeng.theme'
const modeStorageKey = 'dengdeng.color-mode'
const interfaceThemeStorageKey = 'dengdeng.ui-theme'

function isColorMode(value: string | null): value is ColorMode {
  return value === 'light' || value === 'dark' || value === 'system'
}

function isInterfaceTheme(value: string | null): value is InterfaceTheme {
  return value === 'classic' || value === 'control' || value === 'pastel'
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

export const useTheme = defineStore('theme', () => {
  const colorMode = ref<ColorMode>('system')
  const interfaceTheme = ref<InterfaceTheme>('classic')
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
    document.documentElement.dataset.uiTheme = interfaceTheme.value
    delete document.documentElement.dataset.layout
    delete document.documentElement.dataset.density
    document.documentElement.style.colorScheme = nextMode
    const themeColor = interfaceTheme.value === 'control'
      ? nextMode === 'dark' ? '#060907' : '#f1f5f2'
      : interfaceTheme.value === 'pastel'
        ? nextMode === 'dark' ? '#201e1d' : '#fffdfa'
        : nextMode === 'dark' ? '#181613' : '#fffaf1'
    document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
      ?.setAttribute('content', themeColor)
  }

  function setColorMode(next: ColorMode) {
    colorMode.value = next
    writeStorage(modeStorageKey, next)
    writeStorage(legacyModeStorageKey, next === 'system' ? resolveMode(next) : next)
    apply()
  }

  function setInterfaceTheme(next: InterfaceTheme) {
    interfaceTheme.value = next
    writeStorage(interfaceThemeStorageKey, next)
    apply()
  }

  function toggleInterfaceTheme() {
    const themes: InterfaceTheme[] = ['classic', 'control', 'pastel']
    const current = themes.indexOf(interfaceTheme.value)
    setInterfaceTheme(themes[(current + 1) % themes.length])
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

    const savedInterfaceTheme = readStorage(interfaceThemeStorageKey)
    interfaceTheme.value = isInterfaceTheme(savedInterfaceTheme) ? savedInterfaceTheme : 'classic'
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
    interfaceTheme,
    resolvedMode,
    mode: resolvedMode,
    isDark,
    init,
    toggle,
    toggleInterfaceTheme,
    setColorMode,
    setInterfaceTheme,
  }
})
