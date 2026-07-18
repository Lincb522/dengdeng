try {
  const savedTheme = localStorage.getItem('dengdeng.theme')
  document.documentElement.dataset.theme = savedTheme === 'dark' || savedTheme === 'light'
    ? savedTheme
    : (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
} catch {
  // The application theme store applies the fallback after startup when
  // browser privacy settings deny access to localStorage.
}
