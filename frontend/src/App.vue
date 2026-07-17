<script setup lang="ts">
import { onMounted } from 'vue'
import { useAuth } from './stores/auth'
import ToastHost from './components/ToastHost.vue'
import { useTheme } from './stores/theme'

const auth = useAuth()
const theme = useTheme()
onMounted(() => {
  theme.init()
  void auth.loadPublicSettings()
})
</script>

<template>
  <RouterView />
  <ToastHost />
</template>
