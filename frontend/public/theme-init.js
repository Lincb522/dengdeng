try {
  const savedMode = localStorage.getItem('dengdeng.color-mode')
  const legacyMode = localStorage.getItem('dengdeng.theme')
  const mode = savedMode === 'light' || savedMode === 'dark' || savedMode === 'system'
    ? savedMode
    : legacyMode === 'light' || legacyMode === 'dark' ? legacyMode : 'system'
  const resolvedMode = mode === 'system'
    ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
    : mode
  document.documentElement.dataset.theme = resolvedMode
  document.documentElement.style.colorScheme = resolvedMode
  localStorage.removeItem('dengdeng.ui-theme')
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', resolvedMode === 'dark' ? '#181613' : '#fffaf1')
} catch {
  // The application display-mode store applies the fallback after startup when
  // browser privacy settings deny access to localStorage.
}
