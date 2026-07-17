<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api/client'
import type { LegalDocument, PublicSettings } from '../api/types'
import BrandMark from '../components/BrandMark.vue'

const route = useRoute()
const settings = ref<PublicSettings | null>(null)
const loading = ref(true)
const failed = ref(false)

const documentID = computed(() => String(route.params.documentId || ''))
const document = computed<LegalDocument | undefined>(() => settings.value?.login_agreement.documents.find((item) => item.id === documentID.value))

async function load() {
  loading.value = true
  failed.value = false
  try {
    settings.value = await api.get<PublicSettings>('/api/settings')
    document.title = document.value ? `${document.value.title} · ${settings.value.site_name}` : settings.value.site_name
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}

watch(documentID, load)
onMounted(load)
</script>

<template>
  <main class="legal-shell">
    <div class="legal-frame">
      <header class="legal-brand">
        <RouterLink to="/login" class="legal-brand__link" aria-label="返回登录">
          <BrandMark :size="34" />
          <span>{{ settings?.site_name || 'DengDeng AI' }}</span>
        </RouterLink>
        <RouterLink to="/login" class="legal-back">返回登录</RouterLink>
      </header>

      <section v-if="loading" class="legal-loading">正在载入文档…</section>
      <section v-else-if="failed" class="legal-loading">暂时无法载入协议，请返回登录页后重试。</section>
      <section v-else-if="!document" class="legal-loading">没有找到这份协议。</section>
      <article v-else class="legal-document">
        <p class="legal-document__date">更新日期 {{ settings?.login_agreement.updated_at || '—' }}</p>
        <h1>{{ document.title }}</h1>
        <div class="legal-document__content">{{ document.content_md }}</div>
      </article>
    </div>
  </main>
</template>
