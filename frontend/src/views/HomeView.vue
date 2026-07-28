<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { ModelCatalogueItem } from '../api/types'
import { PLATFORM_LABELS } from '../api/types'
import BrandMark from '../components/BrandMark.vue'
import ThemeToggle from '../components/ThemeToggle.vue'
import { useAuth } from '../stores/auth'

const auth = useAuth()
const models = ref<ModelCatalogueItem[]>([])

const availableModels = computed(() => models.value.filter((item) => item.available))
const featuredModels = computed(() => {
  const source = availableModels.value.length ? availableModels.value : models.value
  return source.slice(0, 6)
})

async function loadModels() {
  try {
    const received = await api.get<ModelCatalogueItem[] | null>('/api/models')
    models.value = Array.isArray(received) ? received : []
  } catch {
    models.value = []
  }
}

onMounted(() => {
  void auth.loadPublicSettings()
  void loadModels()
})
</script>

<template>
  <main class="home-shell">
    <header class="home-topbar">
      <RouterLink to="/" class="home-nav-brand" :aria-label="`${auth.siteName} 首页`">
        <img v-if="auth.siteCustomization.logo_url" :src="auth.siteCustomization.logo_url" :alt="auth.siteName" />
        <BrandMark v-else :size="34" />
        <span>
          <strong>{{ auth.siteName }}</strong>
          <small>蹬蹬ai</small>
        </span>
      </RouterLink>
      <nav class="home-nav-actions" aria-label="主页导航">
        <RouterLink v-if="auth.features.model_plaza_enabled" to="/models">模型广场</RouterLink>
        <RouterLink to="/login" class="home-nav-login">登录</RouterLink>
        <ThemeToggle />
      </nav>
    </header>

    <section class="home-stage" aria-labelledby="home-title">
      <div class="home-intro">
        <div class="home-brand-mark" aria-hidden="true">
          <img v-if="auth.siteCustomization.logo_url" :src="auth.siteCustomization.logo_url" :alt="auth.siteName" class="home-custom-logo" />
          <BrandMark v-else :size="94" />
        </div>

        <div class="home-wordmark">
          <h1 id="home-title">{{ auth.siteName }}</h1>
          <p v-if="auth.siteSubtitle">{{ auth.siteSubtitle }}</p>
          <p v-else>OpenAI、Claude 与 Gemini</p>
          <div v-if="auth.siteCustomization.home_content" class="home-custom-content">{{ auth.siteCustomization.home_content }}</div>
        </div>

        <nav class="home-primary-actions" aria-label="主要入口">
          <RouterLink to="/login" class="home-primary-link">登录</RouterLink>
          <RouterLink v-if="auth.features.model_plaza_enabled" to="/models" class="home-secondary-link">查看模型</RouterLink>
        </nav>
      </div>

      <section class="home-model-board" aria-label="可用模型">
        <header>
          <div>
            <strong>模型</strong>
            <span v-if="models.length">{{ availableModels.length }} / {{ models.length }} 可用</span>
            <span v-else>正在获取</span>
          </div>
          <RouterLink v-if="auth.features.model_plaza_enabled" to="/models">全部模型</RouterLink>
        </header>

        <div v-if="featuredModels.length" class="home-model-list">
          <RouterLink
            v-for="(item, index) in featuredModels"
            :key="item.id"
            to="/models"
            class="home-model-row"
            :data-platform="item.platform"
          >
            <span class="home-model-index">{{ String(index + 1).padStart(2, '0') }}</span>
            <span class="home-model-name">
              <strong>{{ item.name }}</strong>
              <small>{{ PLATFORM_LABELS[item.platform] || item.platform }}</small>
            </span>
            <span class="home-model-kind">{{ item.kind === 'image' ? '图像' : '对话' }}</span>
          </RouterLink>
        </div>
        <div v-else class="home-model-loading" aria-hidden="true">
          <span v-for="index in 5" :key="index"></span>
        </div>
      </section>
    </section>

    <footer class="home-footer">
      <span class="home-footer-mark" aria-hidden="true"><BrandMark :size="22" /></span>
      <strong>{{ auth.siteName }}</strong>
      <span>蹬蹬ai</span>
    </footer>
  </main>
</template>
