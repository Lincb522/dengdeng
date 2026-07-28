<script setup lang="ts">
import { computed } from 'vue'
import { useTheme } from '../stores/theme'

const theme = useTheme()
const nextTheme = computed(() => {
  if (theme.interfaceTheme === 'classic') return { label: '切换到信号界面', name: '信号' }
  if (theme.interfaceTheme === 'control') return { label: '切换到柔彩界面', name: '柔彩' }
  return { label: '切换到经典界面', name: '经典' }
})
</script>

<template>
  <button
    type="button"
    class="interface-theme-switcher"
    :class="{ 'is-control': theme.interfaceTheme === 'control', 'is-pastel': theme.interfaceTheme === 'pastel' }"
    :aria-label="nextTheme.label"
    :title="nextTheme.label"
    @click="theme.toggleInterfaceTheme()"
  >
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path v-if="theme.interfaceTheme === 'classic'" d="M3 5h18v14H3V5Zm2 2v2h14V7H5Zm0 4v6h4v-6H5Zm6 0v6h8v-6h-8Z" />
      <path v-else-if="theme.interfaceTheme === 'control'" d="M4 4h16v16H4V4Zm2 2v3h12V6H6Zm0 5v7h12v-7H6Zm2 2h4v2H8v-2Z" />
      <path v-else d="M4 5.5A1.5 1.5 0 0 1 5.5 4h13A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5v-13ZM7 7v4h4V7H7Zm6 0v4h4V7h-4Zm-6 6v4h4v-4H7Zm6 0v4h4v-4h-4Z" />
    </svg>
    <span>{{ nextTheme.name }}</span>
  </button>
</template>
