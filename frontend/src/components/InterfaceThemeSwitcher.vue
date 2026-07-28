<script setup lang="ts">
import { computed } from 'vue'
import { useTheme } from '../stores/theme'

const theme = useTheme()
const label = computed(() => theme.interfaceTheme === 'classic' ? '切换到 Control 界面' : '切换到经典界面')
</script>

<template>
  <button
    type="button"
    class="interface-theme-switcher"
    :class="{ 'is-control': theme.interfaceTheme === 'control' }"
    :aria-label="label"
    :title="label"
    @click="theme.toggleInterfaceTheme()"
  >
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path v-if="theme.interfaceTheme === 'classic'" d="M3 5h18v14H3V5Zm2 2v2h14V7H5Zm0 4v6h4v-6H5Zm6 0v6h8v-6h-8Z" />
      <path v-else d="M4 4h16v16H4V4Zm2 2v3h12V6H6Zm0 5v7h12v-7H6Zm2 2h4v2H8v-2Z" />
    </svg>
    <span>{{ theme.interfaceTheme === 'classic' ? 'Control' : '经典' }}</span>
  </button>
</template>
