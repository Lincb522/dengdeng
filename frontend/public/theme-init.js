try {
  const savedMode = localStorage.getItem('dengdeng.color-mode')
  const legacyMode = localStorage.getItem('dengdeng.theme')
  const mode = savedMode === 'light' || savedMode === 'dark' || savedMode === 'system'
    ? savedMode
    : legacyMode === 'light' || legacyMode === 'dark' ? legacyMode : 'system'
  const resolvedMode = mode === 'system'
    ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
    : mode
  const savedInterfaceTheme = localStorage.getItem('dengdeng.ui-theme')
  const interfaceTheme = savedInterfaceTheme === 'control'
    ? savedInterfaceTheme
    : 'classic'
  if (savedInterfaceTheme === 'pastel') localStorage.setItem('dengdeng.ui-theme', 'classic')
  document.documentElement.dataset.theme = resolvedMode
  document.documentElement.dataset.uiTheme = interfaceTheme
  document.documentElement.style.colorScheme = resolvedMode
  const themeColor = interfaceTheme === 'control'
    ? (resolvedMode === 'dark' ? '#060907' : '#f1f5f2')
    : (resolvedMode === 'dark' ? '#10172a' : '#edf6ff')
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', themeColor)
} catch {
  // The application display-mode store applies the fallback after startup when
  // browser privacy settings deny access to localStorage.
}
