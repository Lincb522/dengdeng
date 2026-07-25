<script setup lang="ts">
import { onMounted } from 'vue'
import BrandMark from '../components/BrandMark.vue'
import ThemeToggle from '../components/ThemeToggle.vue'
import { useAuth } from '../stores/auth'

const auth = useAuth()
onMounted(() => auth.loadPublicSettings())
</script>

<template>
  <main class="home-shell">
    <section class="home-hero" aria-labelledby="home-title">
      <div class="home-brand-mark" aria-hidden="true">
		<img v-if="auth.siteCustomization.logo_url" :src="auth.siteCustomization.logo_url" :alt="auth.siteName" class="home-custom-logo" />
		<BrandMark v-else :size="126" />
      </div>

      <div class="home-wordmark">
		<h1 id="home-title">{{ auth.siteName }}</h1>
		<p v-if="auth.siteSubtitle">{{ auth.siteSubtitle }}</p>
		<span v-if="!auth.siteCustomization.backend_mode_enabled">OpenAI / Claude / Gemini</span>
		<div v-if="auth.siteCustomization.home_content" class="home-custom-content">{{ auth.siteCustomization.home_content }}</div>
      </div>

      <nav class="home-primary-actions" aria-label="主要入口">
        <RouterLink to="/login" class="home-primary-link">登录</RouterLink>
		<RouterLink v-if="auth.features.model_plaza_enabled" to="/models" class="home-secondary-link">模型广场</RouterLink>
      </nav>
    </section>

    <footer class="home-footer">
      <span class="home-footer-mark" aria-hidden="true"><BrandMark :size="22" /></span>
		<strong>{{ auth.siteName }}</strong>
      <span>蹬蹬ai</span>
    </footer>

    <ThemeToggle class="home-theme-toggle" />
  </main>
</template>
