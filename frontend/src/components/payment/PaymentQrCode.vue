<script setup lang="ts">
import QRCode from 'qrcode'
import { ref, watch } from 'vue'

const props = defineProps<{ value: string; label?: string }>()

const source = ref('')
const renderError = ref(false)

async function render(value: string) {
  source.value = ''
  renderError.value = false
  if (!value.trim()) return

  try {
    source.value = await QRCode.toDataURL(value, {
      width: 280,
      margin: 2,
      errorCorrectionLevel: 'M',
      color: { dark: '#2d241f', light: '#fffdf8' },
    })
  } catch {
    renderError.value = true
  }
}

watch(() => props.value, render, { immediate: true })
</script>

<template>
  <div class="payment-qr-code" role="img" :aria-label="label || '支付二维码'">
    <img v-if="source" :src="source" :alt="label || '支付二维码'" width="280" height="280" />
    <span v-else-if="renderError" class="payment-qr-code__error">二维码生成失败，请复制支付码。</span>
    <span v-else class="payment-qr-code__loading">正在生成二维码…</span>
  </div>
</template>
