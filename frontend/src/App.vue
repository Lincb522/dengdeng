<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAuth } from './stores/auth'
import ToastHost from './components/ToastHost.vue'
import ContactBanner from './components/ContactBanner.vue'
import { useTheme } from './stores/theme'

const auth = useAuth()
const theme = useTheme()
const route = useRoute()
const showContactBanner = computed(() => route.name !== 'studio')
onMounted(() => {
  theme.init()
  void auth.loadPublicSettings()
})
</script>

<template>
  <div class="app-frame">
    <ContactBanner v-if="showContactBanner" />
    <div class="app-route">
      <RouterView />
    </div>
  </div>
  <ToastHost />
</template>
