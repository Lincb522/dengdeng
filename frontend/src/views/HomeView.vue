<script setup lang="ts">
import BrandMark from '../components/BrandMark.vue'
import ThemeToggle from '../components/ThemeToggle.vue'
</script>

<template>
  <main class="home-shell">
    <section class="home-hero" aria-labelledby="home-title">
      <div class="home-brand-mark" aria-hidden="true">
        <BrandMark :size="126" />
      </div>

      <div class="home-wordmark">
        <h1 id="home-title">DengDeng AI</h1>
        <p>一个接口，连接常用模型。</p>
        <span>OpenAI / Claude / Gemini</span>
      </div>

      <nav class="home-primary-actions" aria-label="主要入口">
        <RouterLink to="/login" class="home-primary-link">登录</RouterLink>
        <RouterLink to="/models" class="home-secondary-link">模型广场</RouterLink>
      </nav>
    </section>

    <footer class="home-footer">
      <span class="home-footer-mark" aria-hidden="true"><BrandMark :size="22" /></span>
      <strong>DengDeng AI</strong>
      <span>蹬蹬ai</span>
    </footer>

    <ThemeToggle class="home-theme-toggle" />
  </main>
</template>
