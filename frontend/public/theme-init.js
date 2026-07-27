try {
  const savedMode = localStorage.getItem('dengdeng.color-mode')
  const legacyMode = localStorage.getItem('dengdeng.theme')
  const mode = savedMode === 'light' || savedMode === 'dark' || savedMode === 'system'
    ? savedMode
    : legacyMode === 'light' || legacyMode === 'dark' ? legacyMode : 'system'
  const resolvedMode = mode === 'system'
    ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
    : mode
  const savedPreset = localStorage.getItem('dengdeng.ui-theme')
  const preset = ['dengdeng', 'folio', 'signal', 'breeze', 'immersive', 'glass'].includes(savedPreset) ? savedPreset : 'dengdeng'
  const layouts = { dengdeng: 'rail', folio: 'topbar', signal: 'compact', breeze: 'breeze', immersive: 'immersive', glass: 'glass' }
  const densities = { dengdeng: 'comfortable', folio: 'balanced', signal: 'compact', breeze: 'balanced', immersive: 'comfortable', glass: 'balanced' }
  const browserColors = {
    dengdeng: { light: '#fffaf1', dark: '#181613' },
    folio: { light: '#f5f3ef', dark: '#171918' },
    signal: { light: '#f3f7f8', dark: '#0f171a' },
    breeze: { light: '#f1fbf7', dark: '#10221f' },
    immersive: { light: '#eef1f6', dark: '#0e1118' },
    glass: { light: '#e9f2ff', dark: '#111827' },
  }

  document.documentElement.dataset.theme = resolvedMode
  document.documentElement.dataset.uiTheme = preset
  document.documentElement.dataset.layout = layouts[preset]
  document.documentElement.dataset.density = densities[preset]
  document.documentElement.style.colorScheme = resolvedMode
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', browserColors[preset][resolvedMode])
} catch {
  // The application theme store applies the fallback after startup when
  // browser privacy settings deny access to localStorage.
}
