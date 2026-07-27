import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

export type ColorMode = 'light' | 'dark' | 'system'
export type ResolvedColorMode = Exclude<ColorMode, 'system'>
export type ThemeID = 'dengdeng' | 'folio' | 'signal'
export type ThemeLayout = 'rail' | 'topbar' | 'compact'
export type ThemeDensity = 'comfortable' | 'balanced' | 'compact'

export interface ThemePreset {
  id: ThemeID
  name: string
  description: string
  layout: ThemeLayout
  density: ThemeDensity
  colors: [string, string, string]
  browserColors: [string, string]
}

export const themePresets: readonly ThemePreset[] = [
  {
    id: 'dengdeng',
    name: '暖光',
    description: '完整侧栏与舒展内容区',
    layout: 'rail',
    density: 'comfortable',
    colors: ['#fffaf1', '#30261e', '#c98a20'],
    browserColors: ['#fffaf1', '#181613'],
  },
  {
    id: 'folio',
    name: '刊页',
    description: '顶部导航与平面化信息布局',
    layout: 'topbar',
    density: 'balanced',
    colors: ['#f5f3ef', '#202224', '#b94c36'],
    browserColors: ['#f5f3ef', '#171918'],
  },
  {
    id: 'signal',
    name: '信号',
    description: '图标窄栏与紧凑工作区',
    layout: 'compact',
    density: 'compact',
    colors: ['#f3f7f8', '#172126', '#23778a'],
    browserColors: ['#f3f7f8', '#0f171a'],
  },
] as const

const legacyModeStorageKey = 'dengdeng.theme'
const modeStorageKey = 'dengdeng.color-mode'
const presetStorageKey = 'dengdeng.ui-theme'
const defaultPreset: ThemeID = 'dengdeng'

function isColorMode(value: string | null): value is ColorMode {
  return value === 'light' || value === 'dark' || value === 'system'
}

function isThemeID(value: string | null): value is ThemeID {
  return themePresets.some((preset) => preset.id === value)
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
    // The active page still receives the theme when storage is unavailable.
  }
}

export const useTheme = defineStore('theme', () => {
  const colorMode = ref<ColorMode>('system')
  const resolvedMode = ref<ResolvedColorMode>('light')
  const themeID = ref<ThemeID>(defaultPreset)
  const activeTheme = computed(() => themePresets.find((preset) => preset.id === themeID.value) ?? themePresets[0])
  const isDark = computed(() => resolvedMode.value === 'dark')
  let mediaQuery: MediaQueryList | null = null

  function resolveMode(next: ColorMode): ResolvedColorMode {
    if (next !== 'system') return next
    return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }

  function apply() {
    const nextMode = resolveMode(colorMode.value)
    const preset = activeTheme.value
    resolvedMode.value = nextMode
    document.documentElement.dataset.theme = nextMode
    document.documentElement.dataset.uiTheme = preset.id
    document.documentElement.dataset.layout = preset.layout
    document.documentElement.dataset.density = preset.density
    document.documentElement.style.colorScheme = nextMode
    document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
      ?.setAttribute('content', preset.browserColors[nextMode === 'dark' ? 1 : 0])
  }

  function setColorMode(next: ColorMode) {
    colorMode.value = next
    writeStorage(modeStorageKey, next)
    writeStorage(legacyModeStorageKey, next === 'system' ? resolveMode(next) : next)
    apply()
  }

  function setTheme(next: ThemeID) {
    if (!isThemeID(next)) return
    themeID.value = next
    writeStorage(presetStorageKey, next)
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

    const savedPreset = readStorage(presetStorageKey)
    themeID.value = isThemeID(savedPreset) ? savedPreset : defaultPreset

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
    themeID,
    activeTheme,
    isDark,
    init,
    toggle,
    setColorMode,
    setTheme,
  }
})
