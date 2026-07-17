<script setup lang="ts">
import { computed } from 'vue'
import { useTheme } from '../stores/theme'

const theme = useTheme()
const label = computed(() => theme.isDark ? '切换到浅色模式' : '切换到夜间模式')
</script>

<template>
  <button type="button" class="theme-toggle" :aria-label="label" :title="label" @click="theme.toggle()">
    <svg v-if="theme.isDark" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4.75a7.25 7.25 0 1 0 7.25 7.25A7.26 7.26 0 0 0 12 4.75Zm0-2.5v1.2m0 17.1v1.2M4.75 4.75l.85.85m12.8 12.8.85.85M2.25 12h1.2m17.1 0h1.2M4.75 19.25l.85-.85m12.8-12.8.85-.85" /></svg>
    <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="M20.4 14.6A8.2 8.2 0 0 1 9.4 3.6a8.2 8.2 0 1 0 11 11Z" /></svg>
    <span>{{ theme.isDark ? '浅色' : '夜间' }}</span>
  </button>
</template>
