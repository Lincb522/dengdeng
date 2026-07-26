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
const legalDocument = computed<LegalDocument | undefined>(() => settings.value?.login_agreement.documents.find((item) => item.id === documentID.value))

type LegalBlock = { kind: 'heading' | 'paragraph'; text: string }

const legalBlocks = computed<LegalBlock[]>(() => {
  const content = legalDocument.value?.content_md || ''
  const blocks = content
    .replace(/\r\n?/g, '\n')
    .split(/\n{2,}/)
    .map((raw) => {
      const lines = raw.split('\n').map((line) => line.replace(/^\s*#{1,6}\s*/, '').trimEnd())
      const text = lines.join('\n').trim()
      const wasMarkdownHeading = /^\s*#{1,6}\s+/.test(raw)
      const isPlainHeading = !text.includes('\n') && /^(特别提示|重要提示|附则|[一二三四五六七八九十百]+、)/.test(text)
      return text ? { kind: wasMarkdownHeading || isPlainHeading ? 'heading' as const : 'paragraph' as const, text } : null
    })
    .filter((block): block is LegalBlock => block !== null)

  if (blocks[0]?.kind === 'heading' && legalDocument.value && blocks[0].text.includes(legalDocument.value.title)) {
    blocks.shift()
  }
  return blocks
})

async function load() {
  loading.value = true
  failed.value = false
  try {
    settings.value = await api.get<PublicSettings>('/api/settings')
    window.document.title = legalDocument.value ? `${legalDocument.value.title} · ${settings.value.site_name}` : settings.value.site_name
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
      <section v-else-if="!legalDocument" class="legal-loading">没有找到这份协议。</section>
      <article v-else class="legal-document">
        <p class="legal-document__date">更新日期 {{ settings?.login_agreement.updated_at || '—' }}</p>
        <h1>{{ legalDocument.title }}</h1>
        <div class="legal-document__content">
          <template v-for="(block, index) in legalBlocks" :key="`${block.kind}-${index}`">
            <h2 v-if="block.kind === 'heading'">{{ block.text }}</h2>
            <p v-else>{{ block.text }}</p>
          </template>
        </div>
      </article>
    </div>
  </main>
</template>
